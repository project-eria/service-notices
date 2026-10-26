package main

import (
	"errors"
	"flag"
	"os"
	"slices"
	"time"

	"github.com/go-co-op/gocron/v2"

	eria "github.com/project-eria/eria-core"
	"github.com/project-eria/go-wot/dataSchema"
	"github.com/project-eria/go-wot/interaction"
	"github.com/project-eria/go-wot/producer"
	"github.com/project-eria/go-wot/thing"
	zlog "github.com/rs/zerolog/log"
)

var config = struct {
	Subscriptions []Subscription `yaml:"subscriptions"`
}{}

var (
	sources    = []string{"admin", "house", "system"}
	severities = []string{"info", "warning", "alert"}
	audiences  = []string{"all", "profile"}
)

func main() {
	defer func() {
		zlog.Info().Msg("[main] Stopped")
	}()

	// (Config flags should be placed before Init)
	defaultDataPath := "notices.db"
	if env, ok := os.LookupEnv("ERIA_DATA_PATH"); ok {
		defaultDataPath = env
	}
	dataPath := flag.String("data-path", defaultDataPath, "notices database file path")

	eria.Init("ERIA Notices", &config)
	for _, s := range config.Subscriptions {
		if _, ok := severityRank[s.MinSeverity]; !ok || s.Profile == "" || s.URL == "" {
			zlog.Fatal().Str("profile", s.Profile).Str("minSeverity", s.MinSeverity).Msg("[main] invalid subscription (profile, url and minSeverity info|warning|alert)")
		}
	}

	st, err := openStore(*dataPath)
	if err != nil {
		zlog.Fatal().Err(err).Str("file", *dataPath).Msg("[main] could not open the notices database")
	}

	eriaThing, _ := eria.Producer("").AddThing("", setThing())
	setHandlers(eriaThing, st)

	// Each night, forget the notices resolved more than keepResolved ago
	eria.GetCronScheduler().NewJob(
		gocron.DailyJob(1, gocron.NewAtTimes(gocron.NewAtTime(3, 0, 0))),
		gocron.NewTask(func() {
			if n, err := st.purge(time.Now().Add(-keepResolved)); err != nil {
				zlog.Error().Err(err).Msg("[main:purge]")
			} else if n > 0 {
				zlog.Info().Int64("deleted", n).Msg("[main:purge] old resolved notices deleted")
			}
		}),
		gocron.WithTags("purge", "main"),
		gocron.WithStartAt(gocron.WithStartImmediately()),
	)

	eria.Start("")
}

func setThing() *thing.Thing {
	td, _ := eria.NewThingDescription(
		"eria:service:notices",
		eria.AppVersion,
		"Notices",
		"Notifications of the house, sent to the app and to each person's channels",
		[]string{},
	)

	noticesData, _ := dataSchema.NewArray()
	td.AddProperty(interaction.NewProperty(
		"notices",
		"Notices",
		"Latest notices, active and resolved, newest first",
		noticesData,
		interaction.PropertyReadOnly(true),
	))

	readData, _ := dataSchema.NewArray()
	profileVar, _ := dataSchema.NewString()
	td.AddProperty(interaction.NewProperty(
		"read",
		"Read notices",
		"Ids of the notices a profile read",
		readData,
		interaction.PropertyReadOnly(true),
		interaction.PropertyUriVariable("profile", profileVar),
	))

	raiseInput, _ := dataSchema.NewObject()
	raiseOutput, _ := dataSchema.NewString()
	td.AddAction(interaction.NewAction(
		"raise",
		"Raise a notice",
		"Adds a notice {key, source, severity, title, body, audience, thing}, or updates the active one with the same key",
		interaction.ActionInput(&raiseInput),
		interaction.ActionOutput(&raiseOutput),
	))

	resolveInput, _ := dataSchema.NewObject()
	resolveOutput, _ := dataSchema.NewBoolean()
	td.AddAction(interaction.NewAction(
		"resolve",
		"Resolve a notice",
		"Closes the active notice {key}",
		interaction.ActionInput(&resolveInput),
		interaction.ActionOutput(&resolveOutput),
	))

	markReadInput, _ := dataSchema.NewObject()
	td.AddAction(interaction.NewAction(
		"markRead",
		"Mark as read",
		"Records that {profile} read the notices {ids}",
		interaction.ActionInput(&markReadInput),
	))

	noticeData, _ := dataSchema.NewObject()
	td.AddEvent(interaction.NewEvent("raised", "Notice raised", "A new notice", interaction.EventData(&noticeData)))
	td.AddEvent(interaction.NewEvent("resolved", "Notice resolved", "A notice was resolved", interaction.EventData(&noticeData)))
	return td
}

func setHandlers(t producer.ExposedThing, st *store) {
	t.SetPropertyReadHandler("notices", func(producer.ExposedThing, string, map[string]interface{}) (interface{}, error) {
		return st.list()
	})
	readIDs := func(_ producer.ExposedThing, _ string, parameters map[string]interface{}) (interface{}, error) {
		// The presence and the type of `profile` have been checked by the protocol binding
		return st.readIDs(parameters["profile"].(string))
	}
	t.SetPropertyReadHandler("read", readIDs)
	t.SetPropertyObserveHandler("read", readIDs)
	t.SetObserverSelectorHandler("read", func(emitOptions map[string]interface{}, listenerOptions map[string]interface{}) bool {
		return emitOptions["profile"] == listenerOptions["profile"]
	})

	emitList := func() {
		if list, err := st.list(); err == nil {
			t.EmitPropertyChange("notices", list, map[string]interface{}{})
		}
	}

	t.SetActionHandler("raise", func(value interface{}, _ map[string]interface{}) (interface{}, error) {
		n, err := parseRaise(value)
		if err != nil {
			return nil, err
		}
		n, created, err := st.raise(n)
		if err != nil {
			zlog.Error().Err(err).Str("key", n.Key).Msg("[main:raise]")
			return nil, err
		}
		emitList()
		if created {
			t.EmitEvent("raised", n.asMap())
			dispatch(config.Subscriptions, n)
		}
		return n.ID, nil
	})

	t.SetActionHandler("resolve", func(value interface{}, _ map[string]interface{}) (interface{}, error) {
		in, _ := value.(map[string]interface{})
		key, _ := in["key"].(string)
		if key == "" {
			return nil, errors.New("key is required")
		}
		done, err := st.resolve(key)
		if err != nil {
			zlog.Error().Err(err).Str("key", key).Msg("[main:resolve]")
			return nil, err
		}
		if done {
			emitList()
			t.EmitEvent("resolved", map[string]interface{}{"key": key})
		}
		return done, nil
	})

	// ponytail: the profile comes from the input, trusted on the LAN; take it from
	// the token once auth exists (AUTH-DESIGN §2.7).
	t.SetActionHandler("markRead", func(value interface{}, _ map[string]interface{}) (interface{}, error) {
		in, _ := value.(map[string]interface{})
		profile, _ := in["profile"].(string)
		raw, _ := in["ids"].([]interface{})
		if profile == "" {
			return nil, errors.New("profile is required")
		}
		ids := make([]string, 0, len(raw))
		for _, id := range raw {
			if s, ok := id.(string); ok {
				ids = append(ids, s)
			}
		}
		if err := st.markRead(profile, ids); err != nil {
			zlog.Error().Err(err).Str("profile", profile).Msg("[main:markRead]")
			return nil, err
		}
		read, _ := st.readIDs(profile)
		t.EmitPropertyChange("read", read, map[string]interface{}{"profile": profile})
		return nil, nil
	})
}

// parseRaise checks the input of "raise" and fills the defaults
// (source house, severity info, audience profile).
func parseRaise(value interface{}) (Notice, error) {
	in, ok := value.(map[string]interface{})
	if !ok {
		return Notice{}, errors.New("an object is expected")
	}
	str := func(name, def string) string {
		if s, ok := in[name].(string); ok && s != "" {
			return s
		}
		return def
	}
	n := Notice{
		Key:      str("key", ""),
		Source:   str("source", "house"),
		Severity: str("severity", "info"),
		Title:    str("title", ""),
		Body:     str("body", ""),
		Audience: str("audience", "profile"),
		Thing:    str("thing", ""),
	}
	switch {
	case n.Key == "" || n.Title == "":
		return n, errors.New("key and title are required")
	case !slices.Contains(sources, n.Source):
		return n, errors.New("source must be admin, house or system")
	case !slices.Contains(severities, n.Severity):
		return n, errors.New("severity must be info, warning or alert")
	case !slices.Contains(audiences, n.Audience):
		return n, errors.New("audience must be all or profile")
	}
	return n, nil
}
