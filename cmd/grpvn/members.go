package main

import (
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/frane/grpvn/internal"
	"github.com/spf13/cobra"
)

var memberChannels []string
var memberWrite bool
var memberDM bool
var invitePerson, inviteURL, approveName, approveProfile string
var inviteExpiry time.Duration
var inviteUses int

var inviteCmd = &cobra.Command{Use: "invite", Aliases: []string{"iv"}, Short: "Invite a person with scoped channel rights (run on the coordinator)", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
	var link *url.URL
	if inviteURL != "" {
		var err error
		link, err = url.Parse(inviteURL)
		if err != nil || link.Host == "" || (link.Scheme != "http" && link.Scheme != "https") || link.User != nil || link.RawQuery != "" {
			return fmt.Errorf("--url must be the coordinator's http(s) URL")
		}
	}
	db, err := internal.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	channels := append([]string{}, memberChannels...)
	if memberDM {
		channels = append(channels, "@*")
	}
	id, secret, err := internal.CreateInvite(db, invitePerson, channels, memberWrite, inviteExpiry, inviteUses)
	if err != nil {
		return err
	}
	if link != nil {
		link.Fragment = "invite=" + secret
		fmt.Println(link.String())
	} else {
		fmt.Println(secret)
	}
	fmt.Fprintf(os.Stderr, "invite %s for %s; expires in %s; uses=%d; channels=%s; write=%t\n", id, invitePerson, inviteExpiry, inviteUses, strings.Join(memberChannels, ","), memberWrite)
	return nil
}}
var membersCmd = &cobra.Command{Use: "members", Aliases: []string{"mb"}, Short: "Inspect pending people/devices, agents and their rights", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, args []string) error {
	db, err := internal.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	rows, err := db.Query("SELECT id,name,kind,status,approval_code,COALESCE(parent_id,'') FROM principals ORDER BY created_at")
	if err != nil {
		return err
	}
	type member struct{ id, name, kind, status, code, parent string }
	var members []member
	for rows.Next() {
		var m member
		if err := rows.Scan(&m.id, &m.name, &m.kind, &m.status, &m.code, &m.parent); err != nil {
			rows.Close()
			return err
		}
		members = append(members, m)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, m := range members {
		fmt.Printf("%s %s %s %s approval=%s owner=%s", m.id, m.name, m.kind, m.status, m.code, m.parent)
		grants, err := db.Query("SELECT channel,can_write FROM permissions WHERE principal_id=? ORDER BY channel", m.id)
		if err != nil {
			return err
		}
		for grants.Next() {
			var channel string
			var write bool
			if err := grants.Scan(&channel, &write); err != nil {
				grants.Close()
				return err
			}
			mode := "read"
			if write {
				mode = "read/write"
			}
			fmt.Printf(" %s:%s", channel, mode)
		}
		err = grants.Err()
		grants.Close()
		if err != nil {
			return err
		}
		fmt.Println()
	}
	requests, err := db.Query(`SELECT a.approval_code,p.id,p.name,c.name,o.redirect_uri FROM oauth_request_approvals a JOIN oauth_requests o ON o.id=a.request_id JOIN principals p ON p.id=o.principal_id JOIN oauth_clients c ON c.id=o.client_id WHERE a.approved=0 AND o.consumed=0 AND o.expires_at>? ORDER BY o.id`, time.Now().UnixMilli())
	if err != nil {
		return err
	}
	defer requests.Close()
	for requests.Next() {
		var code, id, name, client, redirect string
		if err := requests.Scan(&code, &id, &name, &client, &redirect); err != nil {
			return err
		}
		fmt.Printf("oauth-request approval=%s profile=%s name=%q client=%q redirect=%q\n", code, id, name, client, redirect)
	}
	if err := requests.Err(); err != nil {
		return err
	}
	return nil
}}
var approveCmd = &cobra.Command{Use: "approve <code>", Aliases: []string{"ap"}, Short: "Admit a workspace member; defaults to #general, DMs and normal collaboration", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	db, err := internal.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	channels := append([]string{}, memberChannels...)
	if memberDM {
		channels = append(channels, "@*")
	}
	write := memberWrite
	if !cmd.Flags().Changed("channel") && !cmd.Flags().Changed("write") && !cmd.Flags().Changed("dm") {
		channels = []string{"#general", "@*"}
		write = true
	}
	if err := internal.ApproveMember(db, args[0], approveName, channels, write, approveProfile); err != nil {
		return err
	}
	fmt.Println("approved")
	return nil
}}
var grantCmd = &cobra.Command{Use: "grant <profile-id>", Aliases: []string{"gr"}, Short: "Grant channel rights to an existing profile; no sign-in code or reconnect needed", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	db, err := internal.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	channels := append([]string{}, memberChannels...)
	if memberDM {
		channels = append(channels, "@*")
	}
	if err := internal.GrantWorkspaceChannels(db, args[0], channels, memberWrite); err != nil {
		return err
	}
	fmt.Println("granted; effective immediately")
	return nil
}}
var revokeCmd = &cobra.Command{Use: "revoke <member-or-invite-id>", Aliases: []string{"rv"}, Short: "Revoke a member and its agents, or an invitation", Args: cobra.ExactArgs(1), RunE: func(cmd *cobra.Command, args []string) error {
	db, err := internal.OpenDB()
	if err != nil {
		return err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	result, err := tx.Exec("UPDATE principals SET status='revoked' WHERE id=?", args[0])
	if err != nil {
		return err
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		result, err = tx.Exec("UPDATE invites SET revoked=1 WHERE id=?", args[0])
		if err != nil {
			return err
		}
		n, _ = result.RowsAffected()
	}
	if n == 0 {
		return fmt.Errorf("unknown member or invite ID")
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	fmt.Println("revoked")
	_ = internal.TouchChatMetadata(db)
	return nil
}}

func init() {
	inviteCmd.Flags().StringVar(&invitePerson, "to", "", "person/device receiving this invitation")
	inviteCmd.Flags().StringVar(&inviteURL, "url", "", "coordinator URL; emit a shareable invite link")
	inviteCmd.Flags().DurationVar(&inviteExpiry, "expires", 24*time.Hour, "invitation lifetime")
	inviteCmd.Flags().IntVar(&inviteUses, "uses", 1, "number of device registrations allowed")
	for _, cmd := range []*cobra.Command{inviteCmd, approveCmd, grantCmd} {
		cmd.Flags().StringSliceVar(&memberChannels, "channel", nil, "explicit channel grants (repeat; * means all)")
		cmd.Flags().BoolVar(&memberWrite, "write", false, "allow posting; default read-only")
		cmd.Flags().BoolVar(&memberDM, "dm", false, "allow agent-to-agent DMs (read access is limited to each agent's own inbox)")
	}
	approveCmd.Flags().StringVar(&approveName, "name", "", "human/device label")
	approveCmd.Flags().StringVar(&approveProfile, "profile", "", "reconnect this OAuth request to an existing agent ID")
	rootCmd.AddCommand(inviteCmd, membersCmd, approveCmd, revokeCmd, grantCmd)
}
