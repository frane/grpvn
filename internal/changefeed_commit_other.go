//go:build !windows

package internal

import "context"

func watchSQLiteCommits(context.Context, string, func()) {}
