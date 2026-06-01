package render

import (
	"fmt"
	"io"
	"strings"
	"time"

	"grpvn/internal/msg"
)

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

func HumanHeader(w io.Writer, enabled bool) {
	fmt.Fprintf(w, "%s │ %s │ %s │ %s │ %s\n",
		C("   id", ColorDim, enabled),
		C("when", ColorDim, enabled),
		C("to  ", ColorDim, enabled),
		C("from", ColorDim, enabled),
		"body")
}

func Human(w io.Writer, m *msg.Message, selfName string, colorEnabled bool) {
	id := m.ID
	when := RelativeTime(m.CreatedAt)

	target := m.Target
	targetColor := ColorCyan
	if strings.HasPrefix(target, "@") {
		targetColor = ColorMagenta
	}
	if target == "@"+selfName {
		target = "@me"
	}

	sender := m.Sender
	body := string(m.Body)

	trailer := ""
	if m.ParentID != nil {
		trailer = " reply:" + *m.ParentID
	} else if m.Correlation != nil && *m.Correlation != m.ID {
		trailer = " reply:" + *m.Correlation
	}

	lines := strings.Split(body, "\n")

	fmt.Fprintf(w, "%s │ %4s │ %-6s │ %-6s │ %s\n",
		C(id, ColorDim, colorEnabled),
		C(when, ColorDim, colorEnabled),
		C(fmt.Sprintf("%-6s", target), targetColor, colorEnabled),
		C(fmt.Sprintf("%-6s", sender), ColorBold, colorEnabled),
		lines[0]+trailer)

	for i := 1; i < len(lines); i++ {
		lineOut := "                           │      │        │        │  " + lines[i]
		if i == len(lines)-1 {
			lineOut += trailer
		}
		fmt.Fprintln(w, lineOut)
	}
}
