package autostart

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestSetAndState(t *testing.T) {
	e := Entry{Name: fmt.Sprintf("MoeFrp-test-%d", time.Now().UnixNano()), Exe: `C:\Program Files\MoeFrp\MoeFrp.exe`}
	t.Cleanup(func() { _ = e.Set(false) })
	if exists, _, err := e.State(); err != nil || exists {
		t.Fatalf("initial: %v %v", exists, err)
	}
	if err := e.Set(true); err != nil {
		t.Fatal(err)
	}
	exists, current, err := e.State()
	if err != nil || !exists || !current {
		t.Fatalf("after enable: %v %v %v", exists, current, err)
	}
	moved := Entry{Name: e.Name, Exe: `D:\elsewhere\MoeFrp.exe`}
	if _, current, _ := moved.State(); current {
		t.Fatal("entry pointing to another exe should not be current")
	}
	if err := e.Set(false); err != nil {
		t.Fatal(err)
	}
	if exists, _, _ := e.State(); exists {
		t.Fatal("still exists after disable")
	}
	if err := e.Set(false); err != nil { // 幂等
		t.Fatal(err)
	}
}

func TestLaunched(t *testing.T) {
	if !Launched([]string{"x", "--AutoStart"}) || Launched(os.Args[:1]) {
		t.Fatal("Launched")
	}
}
