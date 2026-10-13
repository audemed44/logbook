package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/logbook/internal/collect"
)

// config is the limits and knobs from the environment. Every limit has a
// default, so Logbook is bounded even when nothing is set.
type config struct {
	maxBytes    int64
	retention   time.Duration
	limits      collect.Limits
	maxLine     int
	backfill    time.Duration
	exclude     map[string]bool
	burstErrors int
}

func loadConfig() (config, error) {
	c := config{exclude: map[string]bool{}}
	var err error
	if c.maxBytes, err = bytesEnv("LOGBOOK_MAX_SIZE", "256MB"); err != nil {
		return c, err
	}
	if c.maxBytes < 16<<20 {
		return c, fmt.Errorf("LOGBOOK_MAX_SIZE: at least 16MB")
	}
	if c.retention, err = daysEnv("LOGBOOK_RETENTION", "30d"); err != nil {
		return c, err
	}
	if c.backfill, err = daysEnv("LOGBOOK_BACKFILL", "1h"); err != nil {
		return c, err
	}
	for _, x := range []struct {
		key  string
		def  int
		into *int
	}{
		{"LOGBOOK_DAILY_QUOTA", 20000, &c.limits.DailyQuota},
		{"LOGBOOK_PER_MINUTE", 600, &c.limits.PerMinute},
		{"LOGBOOK_PER_MESSAGE", 30, &c.limits.PerMessage},
		{"LOGBOOK_MAX_LINE", 8192, &c.maxLine},
		{"LOGBOOK_ALERT_ERRORS", 10, &c.burstErrors},
	} {
		if *x.into, err = intEnv(x.key, x.def); err != nil {
			return c, err
		}
	}
	if c.maxLine < 256 || c.maxLine > 64<<10 {
		return c, fmt.Errorf("LOGBOOK_MAX_LINE: between 256 and 65536")
	}
	for _, name := range strings.Split(os.Getenv("LOGBOOK_EXCLUDE"), ",") {
		if name = strings.TrimSpace(name); name != "" {
			c.exclude[name] = true
		}
	}
	return c, nil
}

func intEnv(key string, def int) (int, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("%s: a whole number, 0 or more", key)
	}
	return n, nil
}

// bytesEnv reads sizes like 256MB, 1GB or 500M.
func bytesEnv(key, def string) (int64, error) {
	v := strings.ToUpper(strings.TrimSpace(os.Getenv(key)))
	if v == "" {
		v = def
	}
	v = strings.TrimSuffix(strings.TrimSuffix(v, "B"), "I")
	mult := int64(1)
	switch {
	case strings.HasSuffix(v, "K"):
		mult = 1 << 10
	case strings.HasSuffix(v, "M"):
		mult = 1 << 20
	case strings.HasSuffix(v, "G"):
		mult = 1 << 30
	}
	n, err := strconv.ParseInt(strings.TrimRight(v, "KMG"), 10, 64)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("%s: a size like 256MB or 1GB", key)
	}
	return n * mult, nil
}

// daysEnv reads 30d, 12h or 90m.
func daysEnv(key, def string) (time.Duration, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		v = def
	}
	if d, ok := strings.CutSuffix(v, "d"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n <= 0 {
			return 0, fmt.Errorf("%s: a duration like 30d or 12h", key)
		}
		return time.Duration(n) * 24 * time.Hour, nil
	}
	dur, err := time.ParseDuration(v)
	if err != nil || dur <= 0 {
		return 0, fmt.Errorf("%s: a duration like 30d or 12h", key)
	}
	return dur, nil
}
