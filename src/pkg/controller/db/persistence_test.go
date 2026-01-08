package db

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/dgraph-io/badger/v4"
	settingsv1 "github.com/q-controller/qcontroller/src/generated/settings/v1"
	vmv1 "github.com/q-controller/qcontroller/src/generated/vm/statemachine/v1"
)

func tempDB(t *testing.T) (*databaseImpl, func()) {
	dir := t.TempDir()
	opts := badger.DefaultOptions(filepath.Join(dir, "badger"))
	db, err := badger.Open(opts)
	if err != nil {
		t.Fatalf("failed to open badger: %v", err)
	}
	return &databaseImpl{db: db}, func() {
		_ = db.Close()
		_ = os.RemoveAll(dir)
	}
}

func validInstance(id string) *vmv1.Instance {
	hw := "aa:bb:cc:dd:ee:ff"
	mem := 1024
	disk := 10
	return &vmv1.Instance{
		Id:   id,
		Path: "/tmp/vm",
		Hardware: &settingsv1.VM{
			Memory: uint32(mem),
			Disk:   uint32(disk),
			Cpus:   2,
		},
		Hwaddr: &hw,
		Pid:    nil,
	}
}

func TestUpdate_ValidInstance(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	inst := validInstance("id1")
	got, err := db.Update(inst)
	if err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	if got.Id != inst.Id {
		t.Errorf("got.Id = %q, want %q", got.Id, inst.Id)
	}
}

func TestUpdate_MissingRequiredFields(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	// Missing Id
	inst := validInstance("")
	_, err := db.Update(inst)
	if err == nil {
		t.Error("expected error for missing Id")
	}
	// Missing Path
	inst = validInstance("id2")
	inst.Path = ""
	_, err = db.Update(inst)
	if err == nil {
		t.Error("expected error for missing Path")
	}
	// Missing Hardware
	inst = validInstance("id3")
	inst.Hardware = nil
	_, err = db.Update(inst)
	if err == nil {
		t.Error("expected error for missing Hardware")
	}
	// Missing Hardware.Memory
	inst = validInstance("id4")
	inst.Hardware.Memory = 0
	_, err = db.Update(inst)
	if err == nil {
		t.Error("expected error for missing Hardware.Memory")
	}
	// Missing Hardware.Disk
	inst = validInstance("id5")
	inst.Hardware.Disk = 0
	_, err = db.Update(inst)
	if err == nil {
		t.Error("expected error for missing Hardware.Disk")
	}
	// Missing Hwaddr
	inst = validInstance("id6")
	inst.Hwaddr = nil
	_, err = db.Update(inst)
	if err == nil {
		t.Error("expected error for missing Hwaddr")
	}
	// Empty Hwaddr
	inst = validInstance("id7")
	empty := ""
	inst.Hwaddr = &empty
	_, err = db.Update(inst)
	if err == nil {
		t.Error("expected error for empty Hwaddr")
	}
}

func TestUpdate_CpusZero(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	inst := validInstance("id8")
	inst.Hardware.Cpus = 0
	_, err := db.Update(inst)
	if err == nil {
		t.Error("expected error for cpus == 0")
	}
}

func TestUpdate_HwaddrUniqueness(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	inst1 := validInstance("id9")
	inst2 := validInstance("id10")
	inst2.Hwaddr = inst1.Hwaddr // same hwaddr
	if _, err := db.Update(inst1); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	_, err := db.Update(inst2)
	if err == nil {
		t.Error("expected error for duplicate hwaddr")
	}
}

func TestUpdate_PidUniqueness(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	pid := int32(1234)
	inst1 := validInstance("id11")
	inst1.Pid = &pid
	inst2 := validInstance("id12")
	inst2.Pid = &pid // same pid
	inst2.Hwaddr = new(string)
	*inst2.Hwaddr = "ff:ee:dd:cc:bb:aa"
	if _, err := db.Update(inst1); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	_, err := db.Update(inst2)
	if err == nil {
		t.Error("expected error for duplicate pid")
	}
}

func TestUpdate_UpdateExistingInstance(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	inst := validInstance("id13")
	pid := int32(4321)
	inst.Pid = &pid
	if _, err := db.Update(inst); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	// Change Hwaddr and Pid
	newHw := "11:22:33:44:55:66"
	newPid := int32(5678)
	inst.Hwaddr = &newHw
	inst.Pid = &newPid
	if _, err := db.Update(inst); err != nil {
		t.Fatalf("Update (change hwaddr/pid) failed: %v", err)
	}
	// Old hwaddr and pid should be free for reuse
	inst2 := validInstance("id14")
	inst2.Hwaddr = new(string)
	*inst2.Hwaddr = "aa:bb:cc:dd:ee:ff"
	inst2.Pid = new(int32)
	*inst2.Pid = 4321
	if _, err := db.Update(inst2); err != nil {
		t.Fatalf("Update with reused old hwaddr/pid failed: %v", err)
	}
}

func TestUpdate_ReuseHwaddrAndPidForSameInstance(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	inst := validInstance("id15")
	pid := int32(9999)
	inst.Pid = &pid
	if _, err := db.Update(inst); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	// Update with same hwaddr and pid (should succeed)
	if _, err := db.Update(inst); err != nil {
		t.Fatalf("Update with same hwaddr/pid failed: %v", err)
	}
}
