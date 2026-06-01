package render

import (
	"fmt"
	"os"

	"github.com/mattn/go-isatty"
)

var (
	ColorDim     = "\033[2m"
	ColorBold    = "\033[1m"
	ColorCyan    = "\033[36m"
	ColorMagenta = "\033[35m"
	ColorReset   = "\033[0m"
)

func ShouldColor(colorFlag string) bool {
	switch colorFlag {
	case "always":
		return true
	case "never":
		return false
	default:
		return isatty.IsTerminal(os.Stdout.Fd())
	}
}

func C(s string, color string, enabled bool) string {
	if !enabled {
		return s
	}
	return fmt.Sprintf("%s%s%s", color, s, ColorReset)
}
