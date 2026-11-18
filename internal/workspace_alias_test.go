package internal

import "testing"

func TestWorkspaceAliasesAreReadableDistinctAndStable(t *testing.T) {
	db := newTestDB(t)
	first := NewMessage("", "", nil).ID
	second := NewMessage("", "", nil).ID
	alias, err := chooseWorkspaceAlias(db, "https://grpvn.acme.test", first, "")
	if err != nil || alias != "acme" {
		t.Fatal(alias, err)
	}
	if _, err := db.Exec("INSERT INTO remotes(name,url,server_id) VALUES('acme','https://grpvn.acme.test',?)", first); err != nil {
		t.Fatal(err)
	}
	if alias, err = chooseWorkspaceAlias(db, "https://chat.acme.test", second, ""); err != nil || alias != "acme-2" {
		t.Fatal("different server reused the same alias", alias, err)
	}
	if _, err = chooseWorkspaceAlias(db, "https://chat.acme.test", second, "acme"); err == nil {
		t.Fatal("a chosen alias silently switched servers")
	}
	if alias, err = chooseWorkspaceAlias(db, "https://grpvn.acme.test", first, "new-name"); err != nil || alias != "acme" {
		t.Fatal("reconnecting changed existing routing", alias, err)
	}
	if alias, err = chooseWorkspaceAlias(db, "https://other.example.test", second, "work"); err != nil || alias != "work" {
		t.Fatal("chosen alias ignored", alias, err)
	}
}
