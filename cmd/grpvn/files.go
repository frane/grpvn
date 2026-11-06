package main

import (
	"fmt"
	"io"
	"os"
	"unicode/utf8"

	"github.com/frane/grpvn/internal"
	"github.com/spf13/cobra"
)

func init() {
	var output string
	command := &cobra.Command{Use: "file <id> <#channel|@user>", Aliases: []string{"fi"}, Short: "Read an attachment, or save it to a new file with --out", Args: cobra.ExactArgs(2), RunE: func(cmd *cobra.Command, args []string) error {
		_, _, db, err := session()
		if err != nil {
			return err
		}
		defer db.Close()
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
	rootCmd.AddCommand(command)
}
