package verbs

import (
	"fmt"
	"io"
	"os"
)

func ID(w io.Writer, name string) {
	cwd, _ := os.Getwd()
	fmt.Fprintf(w, "%s@%s\n", name, cwd)
}
