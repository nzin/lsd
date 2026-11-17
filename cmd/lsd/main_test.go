package main

import "testing"

func TestSelfMonitorIsOnByDefault(t *testing.T) {
	var level string
	f := serverCmd(&level).Flags().Lookup("self-monitor")
	if f == nil {
		t.Fatal("the server has no --self-monitor flag")
	}
	if f.DefValue != "true" {
		t.Fatalf("--self-monitor defaults to %s, want true", f.DefValue)
	}
}

func TestSelfMonitorEnvAndFlag(t *testing.T) {
	var level string

	// the environment variable changes the default...
	t.Setenv("LSD_SELF_MONITOR", "false")
	c := serverCmd(&level)
	if on, _ := c.Flags().GetBool("self-monitor"); on {
		t.Fatal("LSD_SELF_MONITOR=false should turn self-monitoring off")
	}
	// ...and the command line wins over the environment
	if err := c.ParseFlags([]string{"--self-monitor"}); err != nil {
		t.Fatal(err)
	}
	if on, _ := c.Flags().GetBool("self-monitor"); !on {
		t.Fatal("--self-monitor should override LSD_SELF_MONITOR=false")
	}

	// without the variable, the flag alone turns it off
	t.Setenv("LSD_SELF_MONITOR", "")
	c = serverCmd(&level)
	if err := c.ParseFlags([]string{"--self-monitor=false"}); err != nil {
		t.Fatal(err)
	}
	if on, _ := c.Flags().GetBool("self-monitor"); on {
		t.Fatal("--self-monitor=false should turn self-monitoring off")
	}
}
