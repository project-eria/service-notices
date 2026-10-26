package main

import (
	"context"
	"time"

	"github.com/nicholas-fedor/shoutrrr"
	"github.com/nicholas-fedor/shoutrrr/pkg/types"
	zlog "github.com/rs/zerolog/log"
)

// Subscription sends a person's notices to a channel, given as a shoutrrr URL
// (ntfy://…, discord://…, telegram://…, pushover://…). Options of the service
// (ntfy priority, click link…) go in the URL query.
type Subscription struct {
	Profile     string `yaml:"profile"`
	URL         string `yaml:"url"`
	MinSeverity string `yaml:"minSeverity"` // info (default), warning or alert
}

var severityRank = map[string]int{"": 0, "info": 0, "warning": 1, "alert": 2}

// reaches tells whether a notice of this severity goes to a subscription.
func reaches(minSeverity, severity string) bool {
	return severityRank[severity] >= severityRank[minSeverity]
}

// dispatch sends a new notice to the subscriptions it reaches.
// ponytail: every subscribed profile gets "profile" notices too, there is no
// grant check until auth exists (AUTH-DESIGN); WhatsApp/APNs will need their
// own sender beside shoutrrr, chosen by URL scheme.
func dispatch(subs []Subscription, n Notice) {
	for _, s := range subs {
		if !reaches(s.MinSeverity, n.Severity) {
			continue
		}
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			message := n.Body
			if message == "" {
				message = n.Title
			}
			if err := shoutrrr.SendContext(ctx, s.URL, message, &types.Params{types.TitleKey: n.Title}); err != nil {
				zlog.Error().Err(err).Str("profile", s.Profile).Str("notice", n.ID).Msg("[channels:dispatch] send failed")
			}
		}()
	}
}
