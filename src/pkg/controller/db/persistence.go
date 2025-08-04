package db

import (
	"database/sql"
	"errors"
	"fmt"

	settingsv1 "github.com/krjakbrjak/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/krjakbrjak/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/krjakbrjak/qcontroller/src/pkg/controller"
	_ "github.com/mattn/go-sqlite3"
)

var ErrNoInstanceRemoved = errors.New("no instance was removed")

const (
	schemaStmt = `
CREATE TABLE IF NOT EXISTS instances (
	Name TEXT NOT NULL PRIMARY KEY,
	Path TEXT NOT NULL,
	Cpus INTEGER CHECK(Cpus > 0) NOT NULL,
	Memory TEXT NOT NULL,
	Disk TEXT NOT NULL,
	HwAddr TEXT UNIQUE NOT NULL,
	State INTEGER NOT NULL,
	Pid INTEGER NULL UNIQUE
);
`
	getStmt = `
SELECT
	Name,
	Path,
	Cpus,
	Memory,
	Disk,
	HwAddr,
	State,
	Pid FROM instances
`
	getStmtSingle = `
SELECT
	Name,
	Path,
	Cpus,
	Memory,
	Disk,
	HwAddr,
	State,
	Pid FROM instances WHERE Name = ?
`
	upsertStmt = `
INSERT INTO instances (Name, Path, Cpus, Memory, Disk, HwAddr, State, Pid)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(Name) DO UPDATE SET
	Path = COALESCE(excluded.Path, Path),
	Cpus = COALESCE(excluded.Cpus, Cpus),
	Memory = COALESCE(excluded.Memory, Memory),
	Disk = COALESCE(excluded.Disk, Disk),
	HwAddr = COALESCE(excluded.HwAddr, HwAddr),
	State = COALESCE(excluded.State, State),
	Pid = excluded.Pid
`
	deleteStmt = `
DELETE FROM instances WHERE Name = ? AND State = ?
`
)

type databaseImpl struct {
	db *sql.DB
}

func (d *databaseImpl) Get(name string) (result *vmv1.Instance, err error) {
	preparedStmt, stmtErr := d.db.Prepare(getStmtSingle)
	if stmtErr != nil {
		return nil, stmtErr
	}
	defer func() {
		if closeErr := preparedStmt.Close(); closeErr != nil {
			if err == nil {
				// Return close error if no other error
				err = closeErr
			} else {
				err = errors.Join(err, closeErr)
			}
		}
	}()

	var Name string
	var Path string
	var Cpus uint32
	var Memory string
	var Disk string
	var HwAddr string
	var State int
	var Pid sql.NullInt32
	scanErr := preparedStmt.QueryRow(name).Scan(&Name, &Path, &Cpus, &Memory, &Disk, &HwAddr, &State, &Pid)

	if scanErr != nil {
		return nil, scanErr
	}
	var opt *int32
	if Pid.Valid {
		opt = &Pid.Int32
	}
	return &vmv1.Instance{
		Id:    Name,
		Path:  Path,
		State: vmv1.State(State),
		Hardware: &settingsv1.VM{
			Cpus:   Cpus,
			Memory: Memory,
			Disk:   Disk,
		},
		Hwaddr: &HwAddr,
		Pid:    opt,
	}, nil
}

func (d *databaseImpl) List() (result []*vmv1.Instance, err error) {
	preparedStmt, stmtErr := d.db.Prepare(getStmt)
	if stmtErr != nil {
		return nil, stmtErr
	}
	defer func() {
		if closeErr := preparedStmt.Close(); closeErr != nil {
			if err == nil {
				// Return close error if no other error
				err = closeErr
			} else {
				err = errors.Join(err, closeErr)
			}
		}
	}()

	var rows *sql.Rows
	var rowsErr error
	rows, rowsErr = preparedStmt.Query()

	if rowsErr != nil {
		return nil, rowsErr
	}

	instances := []*vmv1.Instance{}

	for rows.Next() {
		instance := &vmv1.Instance{}

		var Name string
		var Path string
		var Cpus uint32
		var Memory string
		var Disk string
		var HwAddr string
		var State int
		var Pid sql.NullInt32
		scanErr := rows.Scan(&Name, &Path, &Cpus, &Memory, &Disk, &HwAddr, &State, &Pid)
		if scanErr != nil {
			return nil, scanErr
		}
		instance.Id = Name
		instance.Path = Path
		instance.State = vmv1.State(State)
		instance.Hardware = &settingsv1.VM{
			Cpus:   Cpus,
			Memory: Memory,
			Disk:   Disk,
		}
		instance.Hwaddr = &HwAddr
		if Pid.Valid {
			instance.Pid = &Pid.Int32
		}
		instances = append(instances, instance)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, rowsErr
	}

	return instances, nil
}

func (d *databaseImpl) Update(instance *vmv1.Instance) (*vmv1.Instance, error) {
	tx, txErr := d.db.Begin()
	if txErr != nil {
		return nil, txErr
	}
	var Id string
	var Path string
	var Cpus uint32
	var Memory string
	var Disk string
	var Hwaddr string
	var State int32
	var Pid sql.NullInt32

	if scanErr := tx.QueryRow(getStmtSingle, instance.Id).Scan(&Id,
		&Path, &Cpus, &Memory, &Disk, &Hwaddr, &State, &Pid); scanErr != nil {
		if scanErr != sql.ErrNoRows {
			return nil, scanErr
		}
	}

	Id = instance.Id

	Path = instance.Path
	if instance.Hardware != nil {
		Cpus = instance.Hardware.Cpus
		Memory = instance.Hardware.Memory
		Disk = instance.Hardware.Disk
	}
	if instance.Hwaddr != nil {
		Hwaddr = *instance.Hwaddr
	}
	State = int32(instance.State)
	if instance.Pid != nil {
		Pid = sql.NullInt32{
			Int32: *instance.Pid,
			Valid: true,
		}
	} else {
		Pid = sql.NullInt32{
			Valid: false,
		}
	}

	if _, stmtErr := tx.Exec(upsertStmt,
		Id,
		Path,
		Cpus,
		Memory,
		Disk,
		Hwaddr,
		State,
		Pid,
	); stmtErr != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return nil, fmt.Errorf("query failed: %v, rollback failed: %v", stmtErr, rbErr)
		}
		return nil, stmtErr
	}

	if scanErr := tx.QueryRow(getStmtSingle, Id).Scan(&Id,
		&Path, &Cpus, &Memory, &Disk, &Hwaddr, &State, &Pid); scanErr != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return nil, fmt.Errorf("query failed: %v, rollback failed: %v", scanErr, rbErr)
		}
		return nil, scanErr
	}

	if commitErr := tx.Commit(); commitErr != nil {
		return nil, commitErr
	}

	var opt *int32
	if Pid.Valid {
		opt = &Pid.Int32
	}
	return &vmv1.Instance{
		Id: Id,
		Hardware: &settingsv1.VM{
			Cpus:   Cpus,
			Memory: Memory,
			Disk:   Disk,
		},
		Hwaddr: &Hwaddr,
		Path:   Path,
		State:  vmv1.State(State),
		Pid:    opt,
	}, nil
}

func (d *databaseImpl) Remove(id string) error {
	result, removeErr := d.db.Exec(deleteStmt, id, vmv1.State_STATE_STOPPED)
	if removeErr != nil {
		return removeErr
	}

	if rowsAffected, err := result.RowsAffected(); err != nil {
		return err
	} else if rowsAffected > 0 {
		return nil
	}

	return ErrNoInstanceRemoved
}

func NewDatabase(path string) (controller.State, error) {
	db, dbErr := sql.Open("sqlite3", path)
	if dbErr != nil {
		return nil, dbErr
	}

	_, schemaErr := db.Exec(schemaStmt)
	if schemaErr != nil {
		return nil, schemaErr
	}

	return &databaseImpl{
		db: db,
	}, nil
}
