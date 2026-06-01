package render

import (
	"fmt"
	"io"
	"strings"

	"grpvn/internal/msg"
)

func AI(w io.Writer, m *msg.Message, selfName string, defaultChannel string, includeTS bool, fullID bool) {
	id := m.ID
	if !fullID {
		id = id[:6]
	}

	target := m.Target
	if target == defaultChannel {
		target = ""
	} else if target == "@"+selfName {
		target = "@me"
	}

	sender := m.Sender
	body := string(m.Body)

	trailer := ""
	if m.ParentID != nil {
		parentID := *m.ParentID
		if !fullID {
			parentID = parentID[:6]
		}
		trailer = " reply:" + parentID
	} else if m.Correlation != nil && *m.Correlation != m.ID {
		corrID := *m.Correlation
		if !fullID {
			corrID = corrID[:6]
		}
		trailer = " reply:" + corrID
	}

	ts := ""
	if includeTS {
		ts = fmt.Sprintf("[%d] ", m.CreatedAt)
	}

	lines := strings.Split(body, "\n")
	out := fmt.Sprintf("%s%s ", ts, id)
	if target != "" {
		out += fmt.Sprintf("[%s] ", target)
	}
	out += fmt.Sprintf("%s: %s", sender, lines[0])

	if len(lines) == 1 {
		fmt.Fprintln(w, out+trailer)
	} else {
		fmt.Fprintln(w, out)
		for i := 1; i < len(lines); i++ {
			lineOut := "  " + lines[i]
			if i == len(lines)-1 {
				lineOut += trailer
			}
			fmt.Fprintln(w, lineOut)
		}
	}
}
