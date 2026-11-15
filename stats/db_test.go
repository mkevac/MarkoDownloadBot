package stats

import (
	"database/sql"
	"path/filepath"
	"sync"
	"testing"
)

// These tests use the package's singleton database and must run serially.
func testDatabase(t *testing.T, legacy bool) *sql.DB {
	t.Helper()
	dir := t.TempDir()
	if legacy {
		oldDB, err := sql.Open("sqlite", filepath.Join(dir, "stats.db"))
		if err != nil {
			t.Fatal(err)
		}
		_, err = oldDB.Exec(`
			CREATE TABLE events (
				id INTEGER PRIMARY KEY AUTOINCREMENT,
				username TEXT,
				event_type TEXT,
				timestamp DATETIME DEFAULT CURRENT_TIMESTAMP
			);
			INSERT INTO events (username, event_type) VALUES
				('', 'video_request'), ('', 'download_error'),
				('reused', 'video_request'), (NULL, 'audio_request');
		`)
		oldDB.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	Init(dir)
	database := getDB()
	t.Cleanup(func() {
		database.Close()
		db = nil
		once = sync.Once{}
		dirBase = ""
	})
	return database
}

func statsByID(t *testing.T, period string) map[int64]UserStats {
	t.Helper()
	s, err := getStats(period)
	if err != nil {
		t.Fatal(err)
	}
	users := make(map[int64]UserStats)
	for _, u := range s.Users {
		if !u.Legacy {
			if _, exists := users[u.UserID]; exists {
				t.Fatalf("multiple stats entries for ID %d", u.UserID)
			}
			users[u.UserID] = u
		}
	}
	return users
}

func TestNamelessSendersInSameGroupStaySeparate(t *testing.T) {
	testDatabase(t, false)
	RegisterUser(-100, 101, "", "Alex", "")
	AddVideoRequest(101, "")
	RegisterUser(-100, 202, "", "Alex", "")
	AddAudioRequest(202, "")

	users := statsByID(t, "overall")
	if len(users) != 2 || users[101].VideoRequests != 1 || users[202].AudioRequests != 1 {
		t.Fatalf("senders were merged or lost: %+v", users)
	}
	if users[101].DisplayName() != "Alex (ID 101)" || users[202].DisplayName() != "Alex (ID 202)" {
		t.Fatalf("duplicate names are not distinguishable: %+v", users)
	}
	chats, err := GetAllUserChatIDs()
	if err != nil || len(chats) != 1 || chats[0] != -100 {
		t.Fatalf("broadcast destination changed: %v, %v", chats, err)
	}
}

func TestRenamesAndReusedUsernamesDoNotChangeIdentity(t *testing.T) {
	testDatabase(t, false)
	RegisterUser(101, 101, "reused", "Old", "Name")
	AddVideoRequest(101, "reused")
	RegisterUser(-100, 101, "new_name", "New", "Name")
	AddAudioRequest(101, "new_name")
	RegisterUser(202, 202, "reused", "Another", "Person")
	AddVideoRequest(202, "reused")
	// A queued download retains the old username snapshot, but the same ID.
	AddDownloadError(101, "reused")
	AddUnrecognizedCommand(202, "reused")

	users := statsByID(t, "overall")
	if len(users) != 2 {
		t.Fatalf("want two identities, got %+v", users)
	}
	if u := users[101]; u.DisplayName() != "@new_name" || u.Counts != (Counts{VideoRequests: 1, AudioRequests: 1, DownloadErrors: 1}) {
		t.Fatalf("renamed user's history changed: %+v", u)
	}
	if u := users[202]; u.DisplayName() != "@reused" || u.Counts != (Counts{VideoRequests: 1, UnrecognizedCommands: 1}) {
		t.Fatalf("reused username merged accounts: %+v", u)
	}

	// A profile update alone changes the label of all that account's events.
	RegisterUser(101, 101, "", "Latest", "Name")
	if u := statsByID(t, "overall")[101]; u.DisplayName() != "Latest Name (ID 101)" || u.Total() != 3 {
		t.Fatalf("removing username lost history or retained a stale label: %+v", u)
	}
}

func TestMigrationPreservesLegacyTotalsWithoutGuessingIdentity(t *testing.T) {
	database := testDatabase(t, true)
	// Starting again must not duplicate or rewrite old rows.
	if err := initializeSchema(database); err != nil {
		t.Fatal(err)
	}
	RegisterUser(101, 101, "reused", "Current", "Owner")
	AddVideoRequest(101, "reused")

	s, err := getStats("overall")
	if err != nil {
		t.Fatal(err)
	}
	want := Counts{VideoRequests: 3, AudioRequests: 1, DownloadErrors: 1}
	if s.Totals() != want || len(s.Users) != 3 {
		t.Fatalf("history lost, duplicated, or assigned to a current user: %+v", s)
	}
	legacy := make(map[string]Counts)
	for _, u := range s.Users {
		if u.Legacy {
			legacy[u.DisplayName()] = u.Counts
		} else if u.UserID != 101 || u.VideoRequests != 1 {
			t.Fatalf("legacy username incorrectly attributed: %+v", u)
		}
	}
	if legacy["Legacy @reused"] != (Counts{VideoRequests: 1}) ||
		legacy["Legacy users without username"] != (Counts{VideoRequests: 1, AudioRequests: 1, DownloadErrors: 1}) {
		t.Fatalf("legacy labels or counts incorrect: %+v", legacy)
	}
	var legacyRows int
	if err := database.QueryRow("SELECT COUNT(*) FROM events WHERE user_id IS NULL").Scan(&legacyRows); err != nil || legacyRows != 4 {
		t.Fatalf("legacy IDs modified: count=%d, err=%v", legacyRows, err)
	}
}

func TestPeriodsIncludeLegacyAndIdentifiedEvents(t *testing.T) {
	database := testDatabase(t, false)
	for _, age := range []string{"-1 hour", "-2 days", "-14 days", "-2 months"} {
		for _, userID := range []any{nil, int64(101)} {
			if _, err := database.Exec("INSERT INTO events (user_id, username, event_type, timestamp) VALUES (?, '', 'video_request', datetime('now', ?))", userID, age); err != nil {
				t.Fatal(err)
			}
		}
	}
	for period, want := range map[string]int{"day": 2, "week": 4, "month": 6, "overall": 8} {
		s, err := getStats(period)
		if err != nil {
			t.Fatal(err)
		}
		if s.Totals().VideoRequests != want || len(s.Users) != 2 {
			t.Errorf("%s: got %+v, want %d videos in two entries", period, s, want)
		}
	}
	if u := statsByID(t, "overall")[101]; u.DisplayName() != "User ID 101" {
		t.Fatalf("missing profile should fall back to ID: %+v", u)
	}
}

func TestNewEventsRejectMissingOrChatIDs(t *testing.T) {
	testDatabase(t, false)
	for _, id := range []int64{0, -100} {
		if err := addEvent(id, "", "video_request"); err == nil {
			t.Errorf("accepted invalid user ID %d", id)
		}
	}
	s, err := getStats("overall")
	if err != nil || len(s.Users) != 0 {
		t.Fatalf("invalid events were stored: %+v, %v", s, err)
	}
}
