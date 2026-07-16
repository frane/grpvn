package main

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

// session is the shared preamble for every verb that touches the store:
// resolve identity, open the DB, and (once) move any pre-v2 cursors out of
// state.json into the cursors table. The caller owns closing the DB.
func session() (string, *internal.State, *sql.DB, error) {
	n, st, err := bootstrap()
	if err != nil {
		return "", nil, nil, err
	}
	db, err := internal.OpenDB()
	if err != nil {
		return "", nil, nil, err
	}
	if err := internal.MigrateLegacyCursors(db, st, internal.ResolveStatePath(statePathFlag)); err != nil {
		db.Close()
		return "", nil, nil, err
	}
	return n, st, db, nil
}

func mustSession() (string, *internal.State, *sql.DB) {
	n, st, db, err := session()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	return n, st, db
}

// unreadNotice prints pending unread to stderr after a non-reading verb, so
// an agent that only ever sends or greps still finds out something is
// waiting. Mirrors the MCP server's result suffix; errors are swallowed —
// a broken count must not fail the verb that ran.
func unreadNotice(db *sql.DB, st *internal.State) {
	line, err := internal.UnreadLine(db, st)
	if err != nil || line == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "[grpvn] unread: %s — run `grpvn r`\n", line)
}

var checkCmd = &cobra.Command{
	Use:     "check",
	Aliases: []string{"c"},
	Run: func(cmd *cobra.Command, args []string) {
		_, st, db := mustSession()
		defer db.Close()
		c, err := internal.Check(os.Stdout, db, st)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(c)
	},
}

var readCmd = &cobra.Command{
	Use:     "read",
	Aliases: []string{"r"},
	Run: func(cmd *cobra.Command, args []string) {
		_, st, db := mustSession()
		defer db.Close()
		c, err := internal.Read(os.Stdout, db, st, countFlag, true, tsFlag, fullFlag, humanFlag, colorFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(c)
	},
}

var peekCmd = &cobra.Command{
	Use:     "peek",
	Aliases: []string{"p"},
	Run: func(cmd *cobra.Command, args []string) {
		_, st, db := mustSession()
		defer db.Close()
		c, err := internal.Read(os.Stdout, db, st, countFlag, false, tsFlag, fullFlag, humanFlag, colorFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		os.Exit(c)
	},
}

var sendCmd = &cobra.Command{
	Use:     "send",
	Aliases: []string{"s"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, db := mustSession()
		defer db.Close()
		t, b, err := parseSendArgs(db, st.DefaultChannel, args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if b, err = expandStdinBody(b); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		m, err := internal.Send(db, n, t, b, st.DefaultChannel, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		autoFollow(st, m.Target)
		unreadNotice(db, st)
	},
}

var askCmd = &cobra.Command{
	Use:     "ask",
	Aliases: []string{"q"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, db := mustSession()
		defer db.Close()
		t, b, err := parseSendArgs(db, st.DefaultChannel, args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if b, err = expandStdinBody(b); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		m, err := internal.Send(db, n, t, b, st.DefaultChannel, true)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(m.ID)
		autoFollow(st, m.Target)
		unreadNotice(db, st)
	},
}

// autoFollow subscribes the sender to a channel it just posted into, so
// replies to its own messages can never land outside its unread. Warns
// instead of failing — the send already committed.
func autoFollow(st *internal.State, target string) {
	added, err := internal.AutoFollow(st, internal.ResolveStatePath(statePathFlag), target)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warn: could not auto-follow %s: %v\n", target, err)
		return
	}
	if added {
		fmt.Fprintf(os.Stderr, "[grpvn] now following %s (posted into it)\n", target)
	}
}

var grepCmd = &cobra.Command{
	Use:     "grep",
	Aliases: []string{"g"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, db := mustSession()
		defer db.Close()
		pat := ""
		scope := ""
		if len(args) > 0 {
			pat = args[0]
		}
		if len(args) > 1 {
			scope = args[1]
		}
		if err := internal.Grep(os.Stdout, db, n, st.Follow, pat, scope, countFlag, st.DefaultChannel, tsFlag, fullFlag, humanFlag, colorFlag); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		unreadNotice(db, st)
	},
}

var logCmd = &cobra.Command{
	Use:     "log",
	Aliases: []string{"l"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, db := mustSession()
		defer db.Close()
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}
		if err := internal.Log(os.Stdout, db, n, arg, countFlag, st.DefaultChannel, tsFlag, fullFlag, humanFlag, colorFlag); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		unreadNotice(db, st)
	},
}

var markCmd = &cobra.Command{
	Use:     "mark",
	Aliases: []string{"m"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, db := mustSession()
		defer db.Close()
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}
		if err := internal.Mark(os.Stdout, db, n, arg, deleteFlag, st.DefaultChannel, tsFlag, fullFlag, humanFlag, colorFlag); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		unreadNotice(db, st)
	},
}

var idCmd = &cobra.Command{
	Use:     "id",
	Aliases: []string{"i"},
	Run: func(cmd *cobra.Command, args []string) {
		n, _, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		internal.ID(os.Stdout, n)
	},
}

var initCmd = &cobra.Command{
	Use:     "init",
	Aliases: []string{"in"},
	Run: func(cmd *cobra.Command, args []string) {
		// Resolve the same way every other verb does (--state, then
		// $GRPVN_STATE, then ~/.grpvn/state.json). The historic cwd-relative
		// default produced a state file no other command would ever read.
		p := internal.ResolveStatePath(statePathFlag)
		n, err := internal.Init(p, asFlag, forceFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(n)
	},
}

var gcOlderThanFlag time.Duration
var gcVacuumFlag bool

var gcCmd = &cobra.Command{
	Use:   "gc",
	Short: "Prune messages older than a cutoff",
	Long: `Deletes messages (and their bookmarks) older than --older-than from the
shared store. Retention is an operator decision: gc exists only on the CLI
and is deliberately not exposed to agents over MCP. Cursors survive pruning
unchanged. Pass --vacuum to compact the file afterwards.`,
	Run: func(cmd *cobra.Command, args []string) {
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		if err := internal.Gc(os.Stdout, db, gcOlderThanFlag, gcVacuumFlag); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

var (
	deleteFlag bool
	forceFlag  bool
)

// expandStdinBody replaces a literal "-" body with the contents of stdin.
// This lives in the CLI layer so the MCP server never reads its own
// transport stream.
func expandStdinBody(b string) (string, error) {
	if b != "-" {
		return b, nil
	}
	data, err := io.ReadAll(io.LimitReader(os.Stdin, internal.MaxBodyBytes+1))
	if err != nil {
		return "", fmt.Errorf("read stdin: %w", err)
	}
	return string(data), nil
}

func parseSendArgs(db *sql.DB, def string, args []string) (string, string, error) {
	if len(args) == 0 {
		return "", "", fmt.Errorf("missing body")
	}
	f := args[0]
	if f == "-" {
		return "", "-", nil
	}
	if strings.HasPrefix(f, "#") || strings.HasPrefix(f, "@") {
		if len(args) < 2 {
			return "", "", fmt.Errorf("missing body")
		}
		return f, args[1], nil
	}
	_, _, err := internal.ResolveTarget(db, f, def)
	if err == nil {
		if len(args) < 2 {
			return "", "", fmt.Errorf("missing body")
		}
		return f, args[1], nil
	}
	return "", f, nil
}

var followCmd = &cobra.Command{
	Use:     "follow",
	Aliases: []string{"f"},
	Short:   "List, add, or remove followed channels",
	Run: func(cmd *cobra.Command, args []string) {
		_, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		p := internal.ResolveStatePath(statePathFlag)
		switch {
		case len(args) == 0:
			for _, f := range st.Follow {
				fmt.Println(f)
			}
		case deleteFlag:
			if len(args) < 1 {
				fmt.Fprintln(os.Stderr, "missing target")
				os.Exit(1)
			}
			st.Follow = removeString(st.Follow, args[0])
			if err := st.Save(p); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		default:
			for _, a := range args {
				if !strings.HasPrefix(a, "#") {
					fmt.Fprintln(os.Stderr, "follow targets must start with #")
					os.Exit(1)
				}
				if !containsString(st.Follow, a) {
					st.Follow = append(st.Follow, a)
				}
			}
			if err := st.Save(p); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		}
	},
}

var defaultCmd = &cobra.Command{
	Use:     "default",
	Aliases: []string{"def"},
	Short:   "Get or set the default channel",
	Run: func(cmd *cobra.Command, args []string) {
		_, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if len(args) == 0 {
			if st.DefaultChannel != "" {
				fmt.Println(st.DefaultChannel)
			}
			return
		}
		ch := args[0]
		if !strings.HasPrefix(ch, "#") {
			fmt.Fprintln(os.Stderr, "default must be a #channel")
			os.Exit(1)
		}
		st.DefaultChannel = ch
		if err := st.Save(internal.ResolveStatePath(statePathFlag)); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

func removeString(xs []string, v string) []string {
	out := xs[:0]
	for _, x := range xs {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

func containsString(xs []string, v string) bool {
	for _, x := range xs {
		if x == v {
			return true
		}
	}
	return false
}
func init() {
	markCmd.Flags().BoolVarP(&deleteFlag, "delete", "d", false, "")
	followCmd.Flags().BoolVarP(&deleteFlag, "delete", "d", false, "")
	initCmd.Flags().BoolVarP(&forceFlag, "force", "f", false, "")
	gcCmd.Flags().DurationVar(&gcOlderThanFlag, "older-than", 0, "prune messages older than this (e.g. 720h)")
	gcCmd.Flags().BoolVar(&gcVacuumFlag, "vacuum", false, "compact the database file after pruning")
	_ = gcCmd.MarkFlagRequired("older-than")
	rootCmd.AddCommand(checkCmd, readCmd, peekCmd, sendCmd, askCmd, grepCmd, logCmd, markCmd, idCmd, initCmd, followCmd, defaultCmd, gcCmd)
}
