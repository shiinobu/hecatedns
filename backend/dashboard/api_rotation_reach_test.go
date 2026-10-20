package dashboard

import (
	"context"
	"errors"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shiinobu/hecatedns/backend/lib/dotnames"
)

func TestRotate_ChecksReachAfterTheNameIsServed(t *testing.T) {
	var events []string
	var inFile []string
	var rig *rotationRig
	rig = newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.Serves = func(context.Context, string) error {
			events = append(events, "served")
			return nil
		}
		c.Rotation.Reaches = func(_ context.Context, host string) error {
			events = append(events, "reached "+host)
			inFile = rig.fileHosts()
			return nil
		}
	})

	code, body := rig.rotate(connMain)

	if code != http.StatusOK {
		t.Fatalf("status %d, body %v", code, body)
	}
	if want := []string{"served", "reached " + rotHost}; !reflect.DeepEqual(events, want) {
		t.Errorf("events = %v, want %v", events, want)
	}
	if !reflect.DeepEqual(inFile, []string{rotHost}) {
		t.Errorf("the names file during the check = %v, want the new name already in it", inFile)
	}
}

func TestRotate_RollsBackWhenTheNameDoesNotReachThisServer(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.Reaches = func(context.Context, string) error { return errors.New("172.67.1.1:853: timeout") }
	})
	rig.seed(dotnames.Entry{Host: rotNameB})

	code, body := rig.rotate(connMain)

	if code != http.StatusBadGateway || body["error"] != "not_reachable" {
		t.Fatalf("status %d, body %v", code, body)
	}
	if got := rig.fileHosts(); !reflect.DeepEqual(got, []string{rotNameB}) {
		t.Errorf("the file was not rolled back: %v", got)
	}
	message, _ := body["message"].(string)
	if !strings.Contains(message, "proxied") || !strings.Contains(message, "Nothing was changed") {
		t.Errorf("message = %q, want the proxy hint and that nothing was changed", message)
	}
	if strings.Contains(message, rotLabel) || strings.Contains(message, "172.67") {
		t.Errorf("the message leaks the name or the address: %q", message)
	}
}

func TestRotate_ReachCheckIsBoundedByItsTimeout(t *testing.T) {
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.ReachWait = 30 * time.Millisecond
		c.Rotation.Reaches = func(ctx context.Context, _ string) error {
			<-ctx.Done()
			return ctx.Err()
		}
	})

	started := time.Now()
	code, body := rig.rotate(connMain)

	if code != http.StatusBadGateway || body["error"] != "not_reachable" || time.Since(started) > 2*time.Second {
		t.Errorf("status %d, body %v, took %v", code, body, time.Since(started))
	}
	if len(rig.fileHosts()) != 0 {
		t.Errorf("the file changed: %v", rig.fileHosts())
	}
}

func TestRotate_NothingIsCheckedForReachWhenTheServerDoesNotServeTheName(t *testing.T) {
	reached := false
	rig := newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.ServeWait = 30 * time.Millisecond
		c.Rotation.Serves = func(context.Context, string) error { return errors.New("not served") }
		c.Rotation.Reaches = func(context.Context, string) error {
			reached = true
			return nil
		}
	})

	code, body := rig.rotate(connMain)

	if code != http.StatusBadGateway || body["error"] != "not_served" || reached {
		t.Errorf("status %d, body %v, reached %v", code, body, reached)
	}
}

func TestRotate_SaysSoWhenTheRollbackAfterAReachFailureFails(t *testing.T) {
	var rig *rotationRig
	rig = newRotationRig(t, func(c *ConnectionOptions) {
		c.Rotation.Reaches = func(context.Context, string) error {
			_ = os.Remove(rig.file)
			_ = os.Mkdir(rig.file, 0o700)
			return errors.New("unreachable")
		}
	})

	code, body := rig.rotate(connMain)

	if code != http.StatusBadGateway || body["error"] != "not_reachable" {
		t.Fatalf("status %d, body %v", code, body)
	}
	if message, _ := body["message"].(string); !strings.Contains(message, "could not be restored") {
		t.Errorf("message = %q, want it to say the names file could not be restored", message)
	}
}
