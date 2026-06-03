package main

import (
	"database/sql"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/frane/grpvn/internal"
)

var checkCmd = &cobra.Command{
	Use:     "check",
	Aliases: []string{"c"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		c, err := internal.Check(os.Stdout, db, n, st.Cursor, st.Follow)
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
		n, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		nx, c, err := internal.Read(os.Stdout, db, n, st.Cursor, st.Follow, countFlag, true, st.DefaultChannel, tsFlag, fullFlag, humanFlag, colorFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if nx != st.Cursor {
			st.Cursor = nx
			p := statePathFlag
			if p == "" {
				p = ".grpvn/state.json"
			}
			if err := st.Save(p); err != nil {
				fmt.Fprintln(os.Stderr, "error:", err)
				os.Exit(1)
			}
		}
		os.Exit(c)
	},
}

var peekCmd = &cobra.Command{
	Use:     "peek",
	Aliases: []string{"p"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		_, c, err := internal.Read(os.Stdout, db, n, st.Cursor, st.Follow, countFlag, false, st.DefaultChannel, tsFlag, fullFlag, humanFlag, colorFlag)
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
		n, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		t, b, err := parseSendArgs(db, st.DefaultChannel, args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if err := internal.Send(db, n, t, b, st.DefaultChannel, false); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

var askCmd = &cobra.Command{
	Use:     "ask",
	Aliases: []string{"q"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		t, b, err := parseSendArgs(db, st.DefaultChannel, args)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if err := internal.Send(db, n, t, b, st.DefaultChannel, true); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

var grepCmd = &cobra.Command{
	Use:     "grep",
	Aliases: []string{"g"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
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
	},
}

var logCmd = &cobra.Command{
	Use:     "log",
	Aliases: []string{"l"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}
		if err := internal.Log(os.Stdout, db, n, arg, countFlag, st.DefaultChannel, tsFlag, fullFlag, humanFlag, colorFlag); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

var markCmd = &cobra.Command{
	Use:     "mark",
	Aliases: []string{"m"},
	Run: func(cmd *cobra.Command, args []string) {
		n, st, err := bootstrap()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		db, err := internal.OpenDB()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		defer db.Close()
		arg := ""
		if len(args) > 0 {
			arg = args[0]
		}
		if err := internal.Mark(os.Stdout, db, n, arg, deleteFlag, st.DefaultChannel, tsFlag, fullFlag, humanFlag, colorFlag); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
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
		p := statePathFlag
		if p == "" {
			p = ".grpvn/state.json"
		}
		n, err := internal.Init(p, asFlag, forceFlag)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		fmt.Println(n)
	},
}

var (
	deleteFlag bool
	forceFlag  bool
)

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
		p := resolveStatePath()
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
		if err := st.Save(resolveStatePath()); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	},
}

func resolveStatePath() string {
	p := statePathFlag
	if p == "" {
		p = os.Getenv("GRPVN_STATE")
	}
	if p == "" {
		p = ".grpvn/state.json"
	}
	return p
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
	rootCmd.AddCommand(checkCmd, readCmd, peekCmd, sendCmd, askCmd, grepCmd, logCmd, markCmd, idCmd, initCmd, followCmd, defaultCmd)
}
