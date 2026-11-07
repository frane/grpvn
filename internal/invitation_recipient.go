package internal

import (
	"database/sql"
	"errors"
	"fmt"
	"net/mail"
	"strings"
)

var errParticipantNotFound = errors.New("Participant not found. Create an invitation for someone new")

type workspaceInvitation struct {
	Kind      string `json:"kind"`
	Status    string `json:"status"`
	Channel   string `json:"channel"`
	Write     bool   `json:"write"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	Recipient string `json:"recipient,omitempty"`
	Email     string `json:"email,omitempty"`
	URL       string `json:"url,omitempty"`
	CLIURL    string `json:"cli_url,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
	Uses      int    `json:"uses,omitempty"`
	Delivery  string `json:"delivery,omitempty"`
}

func invitationRecipient(raw string) (string, string, error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 254 || strings.ContainsAny(raw, "\r\n\x00") {
		return "", "", fmt.Errorf("Enter a name, @handle or email address")
	}
	if strings.Contains(strings.TrimPrefix(raw, "@"), "@") {
		address, err := mail.ParseAddress(raw)
		if err != nil {
			return "", "", fmt.Errorf("Enter one complete email address")
		}
		return address.Address, address.Address, nil
	}
	return raw, "", nil
}

// An existing identity gets a grant. New names and email recipients get a
// scoped invitation; no profile is created until they accept it themselves.
func inviteWorkspaceRecipient(db *sql.DB, access *channelAccess, inviter *Principal, channel, raw string, write bool, base string) (workspaceInvitation, error) {
	recipient, email, err := invitationRecipient(raw)
	if err != nil {
		return workspaceInvitation{}, err
	}
	if recipient != "" && email == "" {
		peer, err := InviteWorkspaceAgent(db, access, inviter, channel, recipient, write)
		if err == nil {
			return workspaceInvitation{Kind: "member", Status: "joined", ID: peer.ID, Name: principalAddress(peer), Channel: channel, Write: write}, nil
		}
		if !errors.Is(err, errParticipantNotFound) {
			return workspaceInvitation{}, err
		}
	}
	if base == "" {
		return workspaceInvitation{}, fmt.Errorf("Set the coordinator public URL to create invitation links")
	}
	secret, err := CreateWorkspaceInvitation(db, access, inviter, channel, write)
	if err != nil {
		return workspaceInvitation{}, err
	}
	base = strings.TrimRight(base, "/")
	return workspaceInvitation{Kind: "invitation", Status: "invitation_ready", Channel: channel, Write: write, Recipient: recipient, Email: email, URL: base + "/join?invite=" + secret, CLIURL: base + "#invite=" + secret, ExpiresIn: 86400, Uses: 1, Delivery: "link"}, nil
}
