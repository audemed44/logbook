package main

import (
	"testing"
	"time"
)

func TestConfig(t *testing.T) {
	c, err := loadConfig()
	if err != nil || c.maxBytes != 256<<20 || c.retention != 30*24*time.Hour || c.limits.DailyQuota != 20000 {
		t.Fatalf("defaults: %v %+v", err, c)
	}
	t.Setenv("LOGBOOK_MAX_SIZE", "1gb")
	t.Setenv("LOGBOOK_RETENTION", "7d")
	t.Setenv("LOGBOOK_EXCLUDE", "kopia, syncthing")
	c, err = loadConfig()
	if err != nil || c.maxBytes != 1<<30 || c.retention != 7*24*time.Hour || !c.exclude["syncthing"] {
		t.Fatalf("set: %v %+v", err, c)
	}
	for k, v := range map[string]string{"LOGBOOK_MAX_SIZE": "1MB", "LOGBOOK_DAILY_QUOTA": "-1", "LOGBOOK_RETENTION": "soon"} {
		t.Setenv(k, v)
		if _, err := loadConfig(); err == nil {
			t.Errorf("%s=%s accepted", k, v)
		}
		t.Setenv(k, "")
	}
}
