package internal

import "testing"

func TestFirstPublicPostJoinsAndWritesInSameMCPRequest(t *testing.T) {
	db := newTestDB(t)
	for _, name := range []string{"creator", "newcomer"} {
		seedTestAgent(t, db, name+"-token", name, []string{"#general", "@*"})
		if _, err := db.Exec("INSERT INTO principal_approvals(principal_id,approved) SELECT id,1 FROM principals WHERE name=?", name); err != nil {
			t.Fatal(err)
		}
	}
	h, err := CoordinatorHandler(db, CoordinatorOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if value, failed := remoteTool(t, h, "creator-token", "create_channel", map[string]any{"channel": "#strategy", "open": true}); failed {
		t.Fatal(value)
	}
	if value, failed := remoteTool(t, h, "newcomer-token", "read", map[string]any{"target": "#strategy"}); failed {
		t.Fatal(value)
	}
	if value, failed := remoteTool(t, h, "newcomer-token", "send", map[string]any{"target": "#strategy", "body": "My first post joins and writes.", "idempotency_key": "first-public-post"}); failed {
		t.Fatal("Public posting required a second request:", value)
	}
	messageIn(t, db, "#strategy", "My first post joins and writes.")
	if _, err := db.Exec("UPDATE permissions SET can_write=0 WHERE channel='#strategy' AND principal_id IN(SELECT id FROM principals WHERE name='newcomer')"); err != nil {
		t.Fatal(err)
	}
	if value, failed := remoteTool(t, h, "newcomer-token", "send", map[string]any{"target": "#strategy", "body": "An explicit read-only restriction still applies."}); !failed {
		t.Fatal("read-only grant bypassed", value)
	}
	if _, err := db.Exec("UPDATE permissions SET can_write=1 WHERE channel='#strategy' AND principal_id IN(SELECT id FROM principals WHERE name='newcomer')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE permissions SET can_write=0 WHERE channel='#strategy' AND principal_id IN(SELECT id FROM principals WHERE name='creator')"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec("UPDATE principals SET parent_id=(SELECT id FROM principals WHERE name='creator') WHERE name='newcomer'"); err != nil {
		t.Fatal(err)
	}
	if value, failed := remoteTool(t, h, "newcomer-token", "send", map[string]any{"target": "#strategy", "body": "An inherited read-only restriction also applies."}); !failed {
		t.Fatal("parent read-only grant bypassed", value)
	}
}
