package main

import (
	"database/sql"
	"encoding/json"
	"strconv"
	"time"

	_ "modernc.org/sqlite"
)

// Notice is a notification, with the JSON fields of the app's Notice
// (eria-ui-app/src/sdk/index.ts); "read" is kept per profile, apart.
type Notice struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	Source   string `json:"source"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Body     string `json:"body,omitempty"`
	At       string `json:"at"`
	Audience string `json:"audience"`
	Thing    string `json:"thing,omitempty"`
	Resolved string `json:"resolved,omitempty"`
}

// asMap gives the notice as WoT event data.
func (n Notice) asMap() map[string]interface{} {
	b, _ := json.Marshal(n)
	m := map[string]interface{}{}
	json.Unmarshal(b, &m)
	return m
}

// An active notice has an empty "resolved"; the partial index keeps one active notice per key.
const schema = `
CREATE TABLE IF NOT EXISTS notices (
	id       INTEGER PRIMARY KEY,
	key      TEXT NOT NULL,
	source   TEXT NOT NULL,
	severity TEXT NOT NULL,
	title    TEXT NOT NULL,
	body     TEXT NOT NULL DEFAULT '',
	at       TEXT NOT NULL,
	audience TEXT NOT NULL,
	thing    TEXT NOT NULL DEFAULT '',
	resolved TEXT NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS notices_active_key ON notices(key) WHERE resolved = '';
CREATE TABLE IF NOT EXISTS reads (
	profile TEXT NOT NULL,
	notice  INTEGER NOT NULL REFERENCES notices(id),
	PRIMARY KEY (profile, notice)
);`

// listLimit is how many notices (active and history) the "notices" property gives.
const listLimit = 100

// keepResolved is how long a resolved notice stays in the database.
const keepResolved = 30 * 24 * time.Hour

type store struct{ db *sql.DB }

func openStore(path string) (*store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// One connection: SQLite writes are serialized anyway, and it avoids SQLITE_BUSY.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	return &store{db}, nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// raise adds a notice, or updates the active one with the same key (its date and
// read state are kept). created tells whether it is a new notice.
func (s *store) raise(n Notice) (out Notice, created bool, err error) {
	tx, err := s.db.Begin()
	if err != nil {
		return n, false, err
	}
	defer tx.Rollback()

	var id int64
	err = tx.QueryRow(`SELECT id, at FROM notices WHERE key = ? AND resolved = ''`, n.Key).Scan(&id, &n.At)
	switch {
	case err == sql.ErrNoRows:
		n.At = now()
		res, err := tx.Exec(`INSERT INTO notices (key, source, severity, title, body, at, audience, thing) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			n.Key, n.Source, n.Severity, n.Title, n.Body, n.At, n.Audience, n.Thing)
		if err != nil {
			return n, false, err
		}
		id, _ = res.LastInsertId()
		created = true
	case err != nil:
		return n, false, err
	default:
		if _, err := tx.Exec(`UPDATE notices SET source = ?, severity = ?, title = ?, body = ?, audience = ?, thing = ? WHERE id = ?`,
			n.Source, n.Severity, n.Title, n.Body, n.Audience, n.Thing, id); err != nil {
			return n, false, err
		}
	}
	n.ID = strconv.FormatInt(id, 10)
	return n, created, tx.Commit()
}

// resolve closes the active notice with this key; false if there was none.
func (s *store) resolve(key string) (bool, error) {
	res, err := s.db.Exec(`UPDATE notices SET resolved = ? WHERE key = ? AND resolved = ''`, now(), key)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// purge deletes the notices resolved before the date, with their read state;
// active notices are never deleted.
func (s *store) purge(before time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	limit := before.UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`DELETE FROM reads WHERE notice IN (SELECT id FROM notices WHERE resolved != '' AND resolved < ?)`, limit); err != nil {
		return 0, err
	}
	res, err := tx.Exec(`DELETE FROM notices WHERE resolved != '' AND resolved < ?`, limit)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, tx.Commit()
}

// list gives the latest notices, newest first.
func (s *store) list() ([]Notice, error) {
	rows, err := s.db.Query(`SELECT id, key, source, severity, title, body, at, audience, thing, resolved FROM notices ORDER BY id DESC LIMIT ?`, listLimit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	list := []Notice{}
	for rows.Next() {
		var n Notice
		if err := rows.Scan(&n.ID, &n.Key, &n.Source, &n.Severity, &n.Title, &n.Body, &n.At, &n.Audience, &n.Thing, &n.Resolved); err != nil {
			return nil, err
		}
		list = append(list, n)
	}
	return list, rows.Err()
}

// markRead records that the profile read these notices; unknown ids are ignored.
func (s *store) markRead(profile string, ids []string) error {
	for _, id := range ids {
		if _, err := s.db.Exec(`INSERT OR IGNORE INTO reads (profile, notice) SELECT ?, id FROM notices WHERE id = ?`, profile, id); err != nil {
			return err
		}
	}
	return nil
}

// readIDs gives the ids of the notices the profile read.
func (s *store) readIDs(profile string) ([]string, error) {
	rows, err := s.db.Query(`SELECT notice FROM reads WHERE profile = ? ORDER BY notice DESC`, profile)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}
