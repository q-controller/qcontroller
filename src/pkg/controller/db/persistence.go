package db

import (
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/dgraph-io/badger/v4"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
	"github.com/q-controller/qcontroller/src/pkg/controller"
)

var ErrNoInstanceRemoved = errors.New("no instance was removed")
var ErrConstraint = errors.New("constraint violation")

const (
	instancePrefix = "instance:"
	hwaddrPrefix   = "hwaddr:"
	pidPrefix      = "pid:"
)

type databaseImpl struct {
	db *badger.DB
}

func (d *databaseImpl) Get(name string) (*vmv1.Instance, error) {
	var inst vmv1.Instance
	err := d.db.View(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(instancePrefix + name))
		if err != nil {
			return err
		}
		return item.Value(func(val []byte) error {
			return json.Unmarshal(val, &inst)
		})
	})
	if err != nil {
		return nil, err
	}
	return &inst, nil
}

func (d *databaseImpl) List() ([]*vmv1.Instance, error) {
	var result []*vmv1.Instance
	err := d.db.View(func(txn *badger.Txn) error {
		opts := badger.DefaultIteratorOptions
		it := txn.NewIterator(opts)
		defer it.Close()
		prefix := []byte(instancePrefix)
		for it.Seek(prefix); it.ValidForPrefix(prefix); it.Next() {
			item := it.Item()
			err := item.Value(func(val []byte) error {
				var inst vmv1.Instance
				if err := json.Unmarshal(val, &inst); err != nil {
					return err
				}
				result = append(result, &inst)
				return nil
			})
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func (d *databaseImpl) Update(instance *vmv1.Instance) (*vmv1.Instance, error) {
	// Enforce NOT NULL and CHECK constraints
	if instance.Id == "" || instance.Path == "" || instance.Hardware == nil ||
		instance.Hardware.Memory == "" || instance.Hardware.Disk == "" ||
		(instance.Hwaddr == nil || *instance.Hwaddr == "") {
		return nil, fmt.Errorf("%w: required field missing", ErrConstraint)
	}
	if instance.Hardware.Cpus == 0 {
		return nil, fmt.Errorf("%w: cpus must be > 0", ErrConstraint)
	}

	// Enforce uniqueness for HwAddr and Pid
	err := d.db.Update(func(txn *badger.Txn) error {
		// Check HwAddr uniqueness
		hwKey := []byte(hwaddrPrefix + *instance.Hwaddr)
		item, err := txn.Get(hwKey)
		if err == nil {
			var existing string
			if err := item.Value(func(val []byte) error {
				existing = string(val)
				return nil
			}); err != nil {
				return err
			}
			if existing != instance.Id {
				return fmt.Errorf("%w: hwaddr not unique", ErrConstraint)
			}
		} else if err != badger.ErrKeyNotFound {
			return err
		}
		// Check Pid uniqueness
		if instance.Pid != nil {
			pidKey := []byte(fmt.Sprintf("%s%d", pidPrefix, *instance.Pid))
			item, err := txn.Get(pidKey)
			if err == nil {
				var existing string
				if err := item.Value(func(val []byte) error {
					existing = string(val)
					return nil
				}); err != nil {
					return err
				}
				if existing != instance.Id {
					return fmt.Errorf("%w: pid not unique", ErrConstraint)
				}
			} else if err != badger.ErrKeyNotFound {
				return err
			}
		}
		// Remove old HwAddr and Pid secondary keys if updating existing instance
		oldInst := &vmv1.Instance{}
		item, err = txn.Get([]byte(instancePrefix + instance.Id))
		if err == nil {
			if err := item.Value(func(val []byte) error {
				return json.Unmarshal(val, oldInst)
			}); err != nil {
				return err
			}
			if oldInst.Hwaddr != nil && *oldInst.Hwaddr != "" && (instance.Hwaddr == nil || *oldInst.Hwaddr != *instance.Hwaddr) {
				if err := txn.Delete([]byte(hwaddrPrefix + *oldInst.Hwaddr)); err != nil && err != badger.ErrKeyNotFound {
					return err
				}
			}
			if oldInst.Pid != nil && (instance.Pid == nil || *oldInst.Pid != *instance.Pid) {
				if err := txn.Delete([]byte(fmt.Sprintf("%s%d", pidPrefix, *oldInst.Pid))); err != nil && err != badger.ErrKeyNotFound {
					return err
				}
			}
		} else if err != badger.ErrKeyNotFound {
			return err
		}
		// Store instance
		data, err := json.Marshal(instance)
		if err != nil {
			return err
		}
		if err := txn.Set([]byte(instancePrefix+instance.Id), data); err != nil {
			return err
		}
		// Set secondary keys
		if instance.Hwaddr != nil && *instance.Hwaddr != "" {
			if err := txn.Set([]byte(hwaddrPrefix+*instance.Hwaddr), []byte(instance.Id)); err != nil {
				return err
			}
		}
		if instance.Pid != nil {
			if err := txn.Set([]byte(fmt.Sprintf("%s%d", pidPrefix, *instance.Pid)), []byte(instance.Id)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return instance, nil
}

func (d *databaseImpl) Remove(id string) error {
	// Only allow remove if state == STATE_STOPPED
	var inst *vmv1.Instance
	err := d.db.Update(func(txn *badger.Txn) error {
		item, err := txn.Get([]byte(instancePrefix + id))
		if err != nil {
			return err
		}
		var tmp vmv1.Instance
		if err := item.Value(func(val []byte) error {
			return json.Unmarshal(val, &tmp)
		}); err != nil {
			return err
		}
		if tmp.State != vmv1.State_STATE_STOPPED {
			return ErrNoInstanceRemoved
		}
		inst = &tmp
		// Remove secondary keys
		if inst.Hwaddr != nil && *inst.Hwaddr != "" {
			if err := txn.Delete([]byte(hwaddrPrefix + *inst.Hwaddr)); err != nil && err != badger.ErrKeyNotFound {
				return err
			}
		}
		if inst.Pid != nil {
			if err := txn.Delete([]byte(fmt.Sprintf("%s%d", pidPrefix, *inst.Pid))); err != nil && err != badger.ErrKeyNotFound {
				return err
			}
		}
		// Remove instance
		return txn.Delete([]byte(instancePrefix + id))
	})
	if err != nil {
		if errors.Is(err, badger.ErrKeyNotFound) {
			return ErrNoInstanceRemoved
		}
		return err
	}
	return nil
}

func NewDatabase(path string) (controller.State, error) {
	opts := badger.DefaultOptions(filepath.Clean(path))
	opts.Logger = nil // Disable badger logging
	db, err := badger.Open(opts)
	if err != nil {
		return nil, err
	}
	return &databaseImpl{db: db}, nil
}
