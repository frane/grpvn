package internal

import (
	"fmt"
	"strings"
)

// Membership changes made by CLI administrators must also reach open clients.
// Read cursors, daemon heartbeats and sync scheduling are deliberately absent.
func installChatChangeTriggers(db dbtx, postgres bool) error {
	tables := []string{"principals", "permissions", "principal_aliases", "channel_subscriptions", "channel_archives", "local_participants", "local_channel_grants", "oauth_request_approvals"}
	var query strings.Builder
	if postgres {
		query.WriteString(`CREATE OR REPLACE FUNCTION grpvn_chat_revision() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 INSERT INTO chat_metadata(key,value) VALUES('revision',1) ON CONFLICT(key) DO UPDATE SET value=chat_metadata.value+1;
 RETURN NULL;
END $$;`)
		for _, table := range tables {
			fmt.Fprintf(&query, "DROP TRIGGER IF EXISTS grpvn_revision_%s ON %s;CREATE TRIGGER grpvn_revision_%s AFTER INSERT OR UPDATE OR DELETE ON %s FOR EACH STATEMENT EXECUTE FUNCTION grpvn_chat_revision();", table, table, table, table)
		}
		query.WriteString("DROP TRIGGER IF EXISTS grpvn_delivery_revision ON remote_messages;CREATE TRIGGER grpvn_delivery_revision AFTER UPDATE OF remote_seq ON remote_messages FOR EACH STATEMENT EXECUTE FUNCTION grpvn_chat_revision();")
	} else {
		for _, table := range tables {
			for _, op := range []string{"INSERT", "UPDATE", "DELETE"} {
				fmt.Fprintf(&query, "CREATE TRIGGER IF NOT EXISTS grpvn_revision_%s_%s AFTER %s ON %s BEGIN INSERT INTO chat_metadata(key,value) VALUES('revision',1) ON CONFLICT(key) DO UPDATE SET value=chat_metadata.value+1; END;", table, op, op, table)
			}
		}
		query.WriteString("CREATE TRIGGER IF NOT EXISTS grpvn_delivery_revision AFTER UPDATE OF remote_seq ON remote_messages WHEN OLD.remote_seq IS NULL AND NEW.remote_seq IS NOT NULL BEGIN INSERT INTO chat_metadata(key,value) VALUES('revision',1) ON CONFLICT(key) DO UPDATE SET value=chat_metadata.value+1; END;")
	}
	_, err := db.Exec(query.String())
	return err
}
