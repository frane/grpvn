package internal

import (
	"database/sql"
	"fmt"
	"io"
)

// A cache target is local to a machine. The coordinator identity and native
// channel identify the conversation when agents communicate across machines.
type ConversationSource struct {
	ServerID string `json:"server_id"`
	URL      string `json:"url"`
	Channel  string `json:"channel"`
	Target   string `json:"local_target"`
}

func conversationSource(db dbtx, target string) *ConversationSource {
	if db == nil {
		return nil
	}
	if handle, ok := db.(*sql.DB); ok && handle == nil {
		return nil
	}
	var source ConversationSource
	err := db.QueryRow(`SELECT r.server_id,r.url,c.channel,c.target FROM remote_channels c JOIN remotes r ON r.name=c.remote_name WHERE c.target=?
 UNION ALL SELECT r.server_id,r.url,'@'||d.peer_name,d.target FROM remote_direct_threads d JOIN remotes r ON r.name=d.remote_name WHERE d.target=?`, target, target).Scan(&source.ServerID, &source.URL, &source.Channel, &source.Target)
	if err != nil {
		return nil
	}
	return &source
}

func (source *ConversationSource) label() string {
	return fmt.Sprintf("remote %s on %s", source.Channel, source.URL)
}

// Keep CLI stdout's acknowledgement/ID contract while surfacing the source
// on stderr. MCP text results include it in the returned content instead.
func WriteConversationSource(w io.Writer, db *sql.DB, target string) {
	if source := conversationSource(db, target); source != nil {
		fmt.Fprintf(w, "[%s; local target %s]\n", source.label(), source.Target)
	}
}
