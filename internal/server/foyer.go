package server

import (
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/audemed44/logbook/internal/parse"
)

// Logbook serves a card in the Foyer widget format
// (https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md):
// errors and warnings in the last 24 hours, storage against the cap, and
// the messages behind the errors.

type foyerStat struct {
	Label   string `json:"label"`
	Value   string `json:"value"`
	Unit    string `json:"unit,omitempty"`
	Caption string `json:"caption,omitempty"`
	Tone    string `json:"tone,omitempty"` // good, warn, bad or accent
}

type foyerItem struct {
	Title    string `json:"title"`
	Subtitle string `json:"subtitle,omitempty"`
	Caption  string `json:"caption,omitempty"`
	URL      string `json:"url,omitempty"`
}

type foyerWidget struct {
	Version     int         `json:"version"`
	Stats       []foyerStat `json:"stats"`
	ItemsTitle  string      `json:"items_title,omitempty"`
	ItemsLayout string      `json:"items_layout,omitempty"`
	Items       []foyerItem `json:"items"`
}

func (s *Server) foyerWidget(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	hours, err := s.Store.Hourly(ctx, time.Now().Add(-23*time.Hour).Truncate(time.Hour), "")
	if err != nil {
		storeError(w, r, err)
		return
	}
	var errs, warns, dropped int
	for _, h := range hours {
		errs += h.Errors
		warns += h.Warns
		dropped += h.Dropped
	}
	usage, err := s.Store.Usage(ctx)
	if err != nil {
		storeError(w, r, err)
		return
	}
	probs, err := s.Store.Problems(ctx, time.Now().Add(-24*time.Hour), parse.Error, 5)
	if err != nil {
		storeError(w, r, err)
		return
	}

	out := foyerWidget{Version: 1, Items: []foyerItem{}, ItemsTitle: "Errors, last 24 hours", ItemsLayout: "list"}
	errTone := "good"
	if errs > 0 {
		errTone = "bad"
	}
	out.Stats = append(out.Stats,
		foyerStat{Label: "Errors", Value: strconv.Itoa(errs), Caption: "24h", Tone: errTone},
		foyerStat{Label: "Warnings", Value: strconv.Itoa(warns), Caption: "24h", Tone: tone(warns > 0, "warn")},
	)
	stored := foyerStat{Label: "Stored", Value: humanBytes(usage.Bytes)}
	if s.Limits.MaxBytes > 0 {
		pct := int(usage.Bytes * 100 / s.Limits.MaxBytes)
		stored.Caption = strconv.Itoa(pct) + "% of " + humanBytes(s.Limits.MaxBytes)
		stored.Tone = tone(pct >= 90, "warn")
	}
	out.Stats = append(out.Stats, stored)
	if dropped > 0 {
		out.Stats = append(out.Stats, foyerStat{Label: "Held back", Value: strconv.Itoa(dropped), Caption: "over the limits", Tone: "warn"})
	}
	for _, p := range probs {
		out.Items = append(out.Items, foyerItem{
			Title: p.Msg, Subtitle: p.Source, Caption: strconv.Itoa(p.Count) + "×",
			URL: "/logs?source=" + url.QueryEscape(p.Source) + "&level=error",
		})
	}
	writeJSON(w, http.StatusOK, out)
}

func tone(on bool, t string) string {
	if on {
		return t
	}
	return ""
}

func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	v := float64(n) / float64(div)
	prec := 1
	if v >= 10 {
		prec = 0
	}
	return strconv.FormatFloat(v, 'f', prec, 64) + " " + string("KMGT"[exp]) + "B"
}
