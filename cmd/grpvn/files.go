package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/frane/grpvn/internal"
	"github.com/spf13/cobra"
)

var sendFiles []string

func sendFileArgs(db *sql.DB, def string, args []string) (string, string, error) {
	if len(sendFiles) > 5 {
		return "", "", fmt.Errorf("attach up to five files per message")
	}
	if len(sendFiles) > 0 {
		if len(args) == 0 {
			args = []string{def, ""}
		}
		if len(args) == 1 {
			if _, _, err := internal.ResolveTarget(db, args[0], def); err == nil {
				args = append(args, "")
			}
		}
	}
	return parseSendArgs(db, def, args)
}

func attachSendFiles(ctx context.Context, db *sql.DB, sender, target, def, body, key string) (string, error) {
	if len(sendFiles) == 0 {
		return body, nil
	}
	conversation, _, err := internal.ResolveTarget(db, target, def)
	if err != nil {
		return "", err
	}
	parts := []string{body}
	for i, path := range sendFiles {
		fileKey := ""
		if key != "" {
			fileKey = fmt.Sprintf("%x", sha256.Sum256([]byte(fmt.Sprintf("%s:file:%d", key, i))))
		}
		file, err := internal.UploadChatFilePath(ctx, db, sender, conversation, path, fileKey)
		if err != nil {
			return "", err
		}
		parts = append(parts, internal.FileMarkdown(file))
	}
	return strings.Join(parts, "\n\n"), nil
}

func init() {
	var output string
	var upload bool
	var key string
	command := &cobra.Command{Use: "file <id|path> <#channel|@user>", Aliases: []string{"fi"}, Short: "Read/save an attachment, or upload a local file with --upload", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		sender, _, db, err := session()
		if err != nil {
			return err
		}
		defer db.Close()
		if upload {
			if output != "" {
				return fmt.Errorf("use --upload or --out, not both")
			}
			file, err := internal.UploadChatFilePath(cmd.Context(), db, sender, args[1], args[0], key)
			if err != nil {
				return err
			}
			return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]any{"id": file.ID, "name": file.Name, "target": file.Target, "type": file.MediaType, "size": len(file.Data), "markdown": internal.FileMarkdown(file)})
		}
		attachment, err := internal.ReadChatFile(cmd.Context(), db, args[0], args[1])
		if err != nil {
			return fmt.Errorf("cannot read this conversation's attachment: %w", err)
		}
		if output != "" {
			file, err := os.OpenFile(output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
			if err != nil {
				return err
			}
			n, writeErr := file.Write(attachment.Data)
			closeErr := file.Close()
			if writeErr != nil {
				return writeErr
			}
			if closeErr != nil {
				return closeErr
			}
			if n != len(attachment.Data) {
				return io.ErrShortWrite
			}
			fmt.Fprintf(cmd.OutOrStdout(), "saved %s (%d bytes)\n", output, n)
			return nil
		}
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s %s %d bytes\n", attachment.ID, attachment.Name, attachment.MediaType, len(attachment.Data))
		if utf8.Valid(attachment.Data) && len(attachment.Data) <= internal.MaxBodyBytes {
			_, err = cmd.OutOrStdout().Write(attachment.Data)
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "Use --out <new-path> to save this file.")
		return nil
	}}
	command.Flags().StringVarP(&output, "out", "o", "", "save bytes to a new path; existing files are never overwritten")
	command.Flags().BoolVarP(&upload, "upload", "u", false, "upload a local path; returns attachment markdown without posting a message")
	command.Flags().StringVar(&key, "idempotency", "", "stable upload retry key")
	for _, send := range []*cobra.Command{sendCmd, askCmd} {
		send.Flags().StringArrayVar(&sendFiles, "file", nil, "attach a local file (repeat; up to 5 files, 10 MB each)")
	}
	rootCmd.AddCommand(command)
}
