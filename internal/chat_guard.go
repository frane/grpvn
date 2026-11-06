package internal

import "database/sql"

// Guards cover older CLI processes sharing this database. Replicated history
// has a durable remote mapping before insertion and can still be imported.
func installSQLiteChatGuard(db *sql.DB) error {
	_, err := db.Exec(`CREATE TRIGGER IF NOT EXISTS grpvn_chat_guard BEFORE INSERT ON messages
WHEN NOT EXISTS(SELECT 1 FROM remote_messages WHERE local_id=NEW.id)
BEGIN
 SELECT RAISE(ABORT,'channel is archived') WHERE EXISTS(SELECT 1 FROM channel_archives WHERE channel=NEW.target);
 SELECT RAISE(ABORT,'local agent is removed or lacks write access') WHERE EXISTS(
  SELECT 1 FROM local_participants p WHERE p.name=NEW.sender AND
   (p.enabled=0 OR (p.restricted=1 AND NOT EXISTS(SELECT 1 FROM local_channel_grants g
    WHERE g.name=p.name AND g.channel IN(NEW.target,'*',CASE WHEN NEW.target LIKE '@%' THEN '@*' ELSE '' END) AND g.can_write=1)))
 );
END`)
	return err
}

const postgresChatGuard = `CREATE OR REPLACE FUNCTION grpvn_chat_guard() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF NOT EXISTS(SELECT 1 FROM remote_messages WHERE local_id=NEW.id) THEN
  IF EXISTS(SELECT 1 FROM channel_archives WHERE channel=NEW.target) THEN RAISE EXCEPTION 'channel is archived'; END IF;
  IF EXISTS(SELECT 1 FROM local_participants p WHERE p.name=NEW.sender AND
   (p.enabled=0 OR (p.restricted=1 AND NOT EXISTS(SELECT 1 FROM local_channel_grants g
    WHERE g.name=p.name AND g.channel IN(NEW.target,'*',CASE WHEN NEW.target LIKE '@%' THEN '@*' ELSE '' END) AND g.can_write=1))))
  THEN RAISE EXCEPTION 'local agent is removed or lacks write access'; END IF;
 END IF;
 RETURN NEW;
END $$;
DROP TRIGGER IF EXISTS grpvn_chat_guard ON messages;
CREATE TRIGGER grpvn_chat_guard BEFORE INSERT ON messages FOR EACH ROW EXECUTE FUNCTION grpvn_chat_guard();`
