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
		Id:      id,
		ImageId: "test-image",
		Hardware: &settingsv1.VM{
			Memory: uint32(mem),
			Disk:   uint32(disk),
			Cpus:   2,
		},
		Hwaddr: &hw,
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
	// Missing ImageId
	inst = validInstance("id2")
	inst.ImageId = ""
	_, err = db.Update(inst)
	if err == nil {
		t.Error("expected error for missing ImageId")
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

func TestUpdate_UpdateExistingInstance(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	inst := validInstance("id13")
	if _, err := db.Update(inst); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	// Change Hwaddr
	newHw := "11:22:33:44:55:66"
	inst.Hwaddr = &newHw
	if _, err := db.Update(inst); err != nil {
		t.Fatalf("Update (change hwaddr) failed: %v", err)
	}
	// Old hwaddr should be free for reuse
	inst2 := validInstance("id14")
	inst2.Hwaddr = new(string)
	*inst2.Hwaddr = "aa:bb:cc:dd:ee:ff"
	if _, err := db.Update(inst2); err != nil {
		t.Fatalf("Update with reused old hwaddr failed: %v", err)
	}
}

func TestUpdate_ReuseHwaddrForSameInstance(t *testing.T) {
	db, cleanup := tempDB(t)
	defer cleanup()
	inst := validInstance("id15")
	if _, err := db.Update(inst); err != nil {
		t.Fatalf("Update failed: %v", err)
	}
	// Update with same hwaddr (should succeed)
	if _, err := db.Update(inst); err != nil {
		t.Fatalf("Update with same hwaddr failed: %v", err)
	}
}
