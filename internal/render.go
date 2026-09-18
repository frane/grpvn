package internal

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-isatty"
)

var (
	ColorDim     = "\033[2m"
	ColorBold    = "\033[1m"
	ColorCyan    = "\033[36m"
	ColorMagenta = "\033[35m"
	ColorReset   = "\033[0m"
)

// UniquePrefixLen returns the shortest ID prefix length (min 6) that keeps
// every message in the batch distinct. The leading ~8 chars of a ULID
// encode the timestamp, so messages minted in the same minute collide at a
// fixed 6 — which turned reply targets into guesswork whenever a
// conversation got busy.
func UniquePrefixLen(msgs []*Message) int {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		ids = append(ids, m.ID)
	}
	sort.Strings(ids)
	n := 6
	for i := 1; i < len(ids); i++ {
		if ids[i] == ids[i-1] {
			continue
		}
		j := 0
		for j < len(ids[i-1]) && j < len(ids[i]) && ids[i-1][j] == ids[i][j] {
			j++
		}
		if j+1 > n {
			n = j + 1
		}
	}
	if n > 26 {
		n = 26
	}
	return n
}

// commonPrefixLen returns how many leading characters a and b share.
func commonPrefixLen(a, b string) int {
	n := 0
	for n < len(a) && n < len(b) && a[n] == b[n] {
		n++
	}
	return n
}

// prefixLenAgainst returns the shortest prefix length (min 6) that tells
// every id in ids apart from every *other* id in universe. universe is
// expected to contain ids.
func prefixLenAgainst(ids, universe []string) int {
	n := 6
	for _, id := range ids {
		for _, other := range universe {
			if other == id {
				continue
			}
			if c := commonPrefixLen(id, other); c+1 > n {
				n = c + 1
			}
		}
	}
	if n > 26 {
		n = 26
	}
	return n
}

// StorePrefixLen returns the ID prefix length to print for this batch: long
// enough that each prefix resolves to exactly one message in the *whole
// store*, not merely within the batch. A batch-unique prefix is not enough,
// because a reply is looked up against every message ever sent (see
// FindMessageByPrefix) — printing one that matches several messages is what
// let a reply land in a channel its author was not working in.
//
// Only messages sharing a batch prefix can collide, so this loads the few
// rows in the same ULID timestamp buckets rather than scanning the store. A
// query failure degrades to the batch-unique length: a prefix that may be
// rejected as ambiguous, never one that silently resolves elsewhere.
func StorePrefixLen(db *sql.DB, msgs []*Message) int {
	batch := make([]string, 0, len(msgs))
	seen := map[string]bool{}
	var buckets []interface{}
	for _, m := range msgs {
		batch = append(batch, m.ID)
		b := m.ID
		if len(b) > storePrefixBucket {
			b = b[:storePrefixBucket]
		}
		if !seen[b] {
			seen[b] = true
			buckets = append(buckets, b)
		}
	}
	if len(buckets) == 0 {
		return 6
	}
	q := "SELECT id FROM messages WHERE substr(id, 1, " + strconv.Itoa(storePrefixBucket) + ") IN (?" + strings.Repeat(", ?", len(buckets)-1) + ")"
	rows, err := db.Query(q, buckets...)
	if err != nil {
		return UniquePrefixLen(msgs)
	}
	defer rows.Close()
	universe := make([]string, 0, len(batch))
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return UniquePrefixLen(msgs)
		}
		universe = append(universe, id)
	}
	if err := rows.Err(); err != nil {
		return UniquePrefixLen(msgs)
	}
	return prefixLenAgainst(batch, universe)
}

// storePrefixBucket is the shortest prefix a read ever prints. Two IDs that
// differ within it can never collide, so it is also the grouping key for the
// collision probe.
const storePrefixBucket = 6

// RenderBatch prints a batch of messages through the selected renderer, with
// AI-mode ID prefixes truncated to the shortest length that still resolves
// to a single message in the store.
func RenderBatch(w io.Writer, db *sql.DB, msgs []*Message, selfName, defaultChannel string, ts, full, human bool, color string) {
	if len(msgs) == 0 {
		return
	}
	if human {
		HumanHeader(w, ShouldColor(color))
		for _, m := range msgs {
			RenderHuman(w, m, selfName, ShouldColor(color))
		}
		return
	}
	plen := UniquePrefixLen(msgs)
	if db != nil {
		plen = StorePrefixLen(db, msgs)
	}
	for _, m := range msgs {
		RenderAI(w, m, selfName, defaultChannel, ts, full, plen)
	}
}

func RenderAI(w io.Writer, m *Message, selfName string, defaultChannel string, includeTS bool, fullID bool, prefLen int) {
	if prefLen < 6 {
		prefLen = 6
	}
	trunc := func(s string) string {
		if fullID || len(s) <= prefLen {
			return s
		}
		return s[:prefLen]
	}
	id := trunc(m.ID)
	target := m.Target
	if target == defaultChannel {
		target = ""
	} else if target == "@"+selfName {
		target = "@me"
	}
	trailer := ""
	if m.ParentID != nil {
		trailer = " reply:" + trunc(*m.ParentID)
	} else if m.Correlation != nil && *m.Correlation != m.ID {
		trailer = " reply:" + trunc(*m.Correlation)
	}
	ts := ""
	if includeTS {
		ts = fmt.Sprintf("[%d] ", m.CreatedAt)
	}
	lines := strings.Split(string(m.Body), "\n")
	out := fmt.Sprintf("%s%s ", ts, id)
	if target != "" {
		out += fmt.Sprintf("[%s] ", target)
	}
	out += fmt.Sprintf("%s: %s", m.Sender, lines[0])
	if len(lines) == 1 {
		fmt.Fprintln(w, out+trailer)
	} else {
		fmt.Fprintln(w, out)
		for i := 1; i < len(lines); i++ {
			lOut := "  " + lines[i]
			if i == len(lines)-1 {
				lOut += trailer
			}
			fmt.Fprintln(w, lOut)
		}
	}
}

func RenderHuman(w io.Writer, m *Message, selfName string, enabled bool) {
	id := m.ID
	when := RelativeTime(m.CreatedAt)
	target := m.Target
	tCol := ColorCyan
	if strings.HasPrefix(target, "@") {
		tCol = ColorMagenta
	}
	if target == "@"+selfName {
		target = "@me"
	}
	trailer := ""
	if m.ParentID != nil {
		trailer = " reply:" + *m.ParentID
	} else if m.Correlation != nil && *m.Correlation != m.ID {
		trailer = " reply:" + *m.Correlation
	}
	lines := strings.Split(string(m.Body), "\n")
	fmt.Fprintf(w, "%s │ %4s │ %-6s │ %-6s │ %s\n", C(id, ColorDim, enabled), C(when, ColorDim, enabled), C(fmt.Sprintf("%-6s", target), tCol, enabled), C(fmt.Sprintf("%-6s", m.Sender), ColorBold, enabled), lines[0]+trailer)
	for i := 1; i < len(lines); i++ {
		lOut := "                           │      │        │        │  " + lines[i]
		if i == len(lines)-1 {
			lOut += trailer
		}
		fmt.Fprintln(w, lOut)
	}
}

func HumanHeader(w io.Writer, enabled bool) {
	fmt.Fprintf(w, "%s │ %s │ %s │ %s │ %s\n", C("   id", ColorDim, enabled), C("when", ColorDim, enabled), C("to  ", ColorDim, enabled), C("from", ColorDim, enabled), "body")
}

func RelativeTime(t int64) string {
	d := time.Since(time.UnixMilli(t))
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm", int(d.Minutes()))
	}
	if d < 24*time.Hour {
		return fmt.Sprintf("%dh", int(d.Hours()))
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

func C(s string, color string, enabled bool) string {
	if !enabled {
		return s
	}
	return fmt.Sprintf("%s%s%s", color, s, ColorReset)
}

func ShouldColor(flag string) bool {
	if flag == "always" {
		return true
	}
	if flag == "never" {
		return false
	}
	return isatty.IsTerminal(os.Stdout.Fd())
}
