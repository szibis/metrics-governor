package ruleactivity

import (
	"bytes"
	"log"
	"strings"
	"testing"
	"time"
)

func TestActivity_NewActivity(t *testing.T) {
	a := NewActivity()
	if a.LoadedTime == 0 {
		t.Error("expected non-zero LoadedTime")
	}
	if a.LastMatchTime.Load() != 0 {
		t.Error("expected zero LastMatchTime initially")
	}
}

func TestActivity_RecordMatch(t *testing.T) {
	a := NewActivity()
	a.RecordMatch()

	if a.LastMatchTime.Load() == 0 {
		t.Error("expected non-zero LastMatchTime after RecordMatch")
	}
}

func TestActivity_IsDead_NeverMatched(t *testing.T) {
	a := &Activity{LoadedTime: time.Now().Add(-2 * time.Second).UnixNano()}
	threshold := time.Second.Nanoseconds()
	now := time.Now().UnixNano()

	if !a.IsDead(now, threshold) {
		t.Error("expected dead: loaded 2s ago, threshold 1s, never matched")
	}
}

func TestActivity_IsDead_NeverMatched_TooSoon(t *testing.T) {
	a := NewActivity() // just loaded
	threshold := time.Second.Nanoseconds()
	now := time.Now().UnixNano()

	if a.IsDead(now, threshold) {
		t.Error("expected alive: just loaded, threshold 1s")
	}
}

func TestActivity_IsDead_MatchedRecently(t *testing.T) {
	a := &Activity{LoadedTime: time.Now().Add(-10 * time.Second).UnixNano()}
	a.LastMatchTime.Store(time.Now().UnixNano()) // just matched

	threshold := time.Second.Nanoseconds()
	now := time.Now().UnixNano()

	if a.IsDead(now, threshold) {
		t.Error("expected alive: matched just now")
	}
}

func TestActivity_IsDead_MatchedLongAgo(t *testing.T) {
	a := &Activity{LoadedTime: time.Now().Add(-10 * time.Second).UnixNano()}
	a.LastMatchTime.Store(time.Now().Add(-5 * time.Second).UnixNano())

	threshold := time.Second.Nanoseconds()
	now := time.Now().UnixNano()

	if !a.IsDead(now, threshold) {
		t.Error("expected dead: matched 5s ago, threshold 1s")
	}
}

func TestActivity_EvaluateAndTransition_AliveToAlive(t *testing.T) {
	a := NewActivity()
	a.RecordMatch()

	now := time.Now().UnixNano()
	threshold := time.Second.Nanoseconds()

	isDead, transitioned, _ := a.EvaluateAndTransition(now, threshold)
	if isDead {
		t.Error("expected alive")
	}
	if transitioned {
		t.Error("expected no transition (alive→alive)")
	}
}

func TestActivity_EvaluateAndTransition_AliveToDead(t *testing.T) {
	a := &Activity{LoadedTime: time.Now().Add(-5 * time.Second).UnixNano()}

	now := time.Now().UnixNano()
	threshold := time.Second.Nanoseconds()

	isDead, transitioned, direction := a.EvaluateAndTransition(now, threshold)
	if !isDead {
		t.Error("expected dead")
	}
	if !transitioned {
		t.Error("expected transition alive→dead")
	}
	if direction != "dead" {
		t.Errorf("expected direction 'dead', got %q", direction)
	}
}

func TestActivity_EvaluateAndTransition_DeadToDead(t *testing.T) {
	a := &Activity{LoadedTime: time.Now().Add(-5 * time.Second).UnixNano()}
	a.WasDead.Store(true)

	now := time.Now().UnixNano()
	threshold := time.Second.Nanoseconds()

	isDead, transitioned, _ := a.EvaluateAndTransition(now, threshold)
	if !isDead {
		t.Error("expected dead")
	}
	if transitioned {
		t.Error("expected no transition (dead→dead)")
	}
}

func TestActivity_EvaluateAndTransition_DeadToAlive(t *testing.T) {
	a := &Activity{LoadedTime: time.Now().Add(-5 * time.Second).UnixNano()}
	a.WasDead.Store(true)
	a.RecordMatch() // revive

	now := time.Now().UnixNano()
	threshold := time.Second.Nanoseconds()

	isDead, transitioned, direction := a.EvaluateAndTransition(now, threshold)
	if isDead {
		t.Error("expected alive")
	}
	if !transitioned {
		t.Error("expected transition dead→alive")
	}
	if direction != "alive" {
		t.Errorf("expected direction 'alive', got %q", direction)
	}
}

func TestLogTransition_NoTransition(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(nil)

	LogTransition("limits", "test-rule", false, false, 5*time.Minute)

	if buf.Len() != 0 {
		t.Errorf("expected no log output, got %q", buf.String())
	}
}

func TestLogTransition_Dead(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(nil)

	LogTransition("limits", "test-rule", true, true, 5*time.Minute)

	out := buf.String()
	if !strings.Contains(out, "[WARN]") {
		t.Errorf("expected WARN log, got %q", out)
	}
	if !strings.Contains(out, "test-rule") {
		t.Errorf("expected rule name in log, got %q", out)
	}
	if !strings.Contains(out, "appears dead") {
		t.Errorf("expected 'appears dead' in log, got %q", out)
	}
	if !strings.Contains(out, "5m0s") {
		t.Errorf("expected threshold in log, got %q", out)
	}
}

func TestLogTransition_Alive(t *testing.T) {
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(nil)

	LogTransition("processing", "revived-rule", false, true, time.Minute)

	out := buf.String()
	if !strings.Contains(out, "[INFO]") {
		t.Errorf("expected INFO log, got %q", out)
	}
	if !strings.Contains(out, "revived-rule") {
		t.Errorf("expected rule name in log, got %q", out)
	}
	if !strings.Contains(out, "alive again") {
		t.Errorf("expected 'alive again' in log, got %q", out)
	}
}
