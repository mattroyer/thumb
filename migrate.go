package main

import (
	"fmt"
	"net/url"
)

// listAll returns every record in a collection, following pagination.
func listAll(pds, did, coll string) ([]planRecord, error) {
	var all []planRecord
	cursor := ""
	for {
		params := url.Values{"repo": {did}, "collection": {coll}, "limit": {"100"}}
		if cursor != "" {
			params.Set("cursor", cursor)
		}
		var r struct {
			Records []planRecord `json:"records"`
			Cursor  string       `json:"cursor"`
		}
		if err := doJSON("GET", pds+"/xrpc/com.atproto.repo.listRecords", params, nil, "", &r); err != nil {
			return nil, err
		}
		all = append(all, r.Records...)
		if r.Cursor == "" || len(r.Records) == 0 {
			return all, nil
		}
		cursor = r.Cursor
	}
}

// cmdMigrate moves your plan entries from an old collection name into the
// current one. Record keys are kept, so dates and ordering are preserved.
// Each entry is copied before the original is deleted, so it's safe to re-run
// after an interruption.
func (a *app) cmdMigrate(args []string) error {
	fs := a.flags("migrate", "thumb migrate [--from collection] [--dry-run] [--keep]")
	from := fs.String("from", legacyCollection, "collection to move entries out of")
	dry := fs.Bool("dry-run", false, "show what would move without changing anything")
	keep := fs.Bool("keep", false, "copy entries but leave the originals in place")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	if *from == collection {
		return fmt.Errorf("--from is the current collection (%s); nothing to do", collection)
	}
	s, err := loadSession()
	if err != nil {
		return err
	}
	recs, err := listAll(s.PDS, s.DID, *from)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		fmt.Fprintf(a.out, "Nothing to migrate: you have no entries in %s.\n", *from)
		return nil
	}

	verb := "Moving"
	if *keep {
		verb = "Copying"
	}
	fmt.Fprintf(a.out, "%s %d entr%s from %s to %s\n", verb, len(recs), plural(len(recs), "y", "ies"), *from, collection)
	for _, r := range recs {
		fmt.Fprintf(a.out, "  %s  %s\n", r.rkey(), fmtTime(r.Value.CreatedAt))
		if *dry {
			continue
		}
		v := r.Value
		v.Type = collection
		err := xrpcAuthed(s, "POST", "com.atproto.repo.putRecord", nil, map[string]any{
			"repo": s.DID, "collection": collection, "rkey": r.rkey(),
			"record": v, "validate": false,
		}, nil)
		if err != nil {
			return fmt.Errorf("copying %s: %v (safe to re-run thumb migrate)", r.rkey(), err)
		}
		if *keep {
			continue
		}
		err = xrpcAuthed(s, "POST", "com.atproto.repo.deleteRecord", nil, map[string]any{
			"repo": s.DID, "collection": *from, "rkey": r.rkey(), "swapRecord": r.CID,
		}, nil)
		if err != nil {
			return fmt.Errorf("removing old copy of %s: %v (safe to re-run thumb migrate)", r.rkey(), err)
		}
	}
	if *dry {
		fmt.Fprintln(a.out, "Dry run; nothing changed.")
	} else {
		fmt.Fprintln(a.out, "Done.")
	}
	return nil
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
