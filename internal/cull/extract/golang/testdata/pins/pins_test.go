package pins

import "testing"

func localHelper() int { return 1 }

func TestConst(t *testing.T) {
	if Setting != 3 {
		t.Fatal("setting")
	}
}

func TestEnum(t *testing.T) {
	if ModeX != Mode(1) {
		t.Fatal("mode")
	}
}

func TestTypeOnly(t *testing.T) {
	var m Mode
	if m != 0 && len(t.Name()) == 0 {
		t.Fatal("mode")
	}
}

func TestCallsProject(t *testing.T) {
	if Helper() != Setting {
		t.Fatal("helper")
	}
}

func TestConstructs(t *testing.T) {
	c := Cause{Kind: "x"}
	if c.Kind != "x" || Setting != 3 {
		t.Fatal("cause")
	}
}

func TestSameFileHelper(t *testing.T) {
	if localHelper() != Setting {
		t.Fatal("local")
	}
}

func TestNoCodeUnderTest(t *testing.T) {
	if 1+1 != 2 {
		t.Fatal("math")
	}
}

func TestSubtests(t *testing.T) {
	t.Run("pins", func(t *testing.T) {
		if Setting != 3 {
			t.Fatal("setting")
		}
	})
	t.Run("calls", func(t *testing.T) {
		if Helper() != 1 {
			t.Fatal("helper")
		}
	})
}
