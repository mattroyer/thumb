package main

import (
	"fmt"
	"sort"
	"sync"
	"time"
)

// cmdNew lists people on your list who have posted since you last read
// their plan. It only reads: thumbing someone is what marks them as seen.
func (a *app) cmdNew(args []string) error {
	fs := a.flags("new", "thumb new")
	if _, err := parseFlags(fs, args); err != nil {
		return err
	}
	list := loadThumbed()
	if len(list) == 0 {
		fmt.Fprintln(a.out, "Your list is empty. Thumb someone first: thumb <handle>")
		return nil
	}

	type result struct {
		handle  string
		posted  time.Time
		checked bool
		err     error
	}
	results := make([]result, len(list))
	slots := make(chan struct{}, 8) // check up to 8 people at once
	var wg sync.WaitGroup
	for i, t := range list {
		if t.Seen.IsZero() {
			continue // added before thumb kept times; it'll count after your next look
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			posted, err := latestPost(t.Handle)
			results[i] = result{handle: t.Handle, posted: posted, checked: true, err: err}
		}()
	}
	wg.Wait()

	var fresh []result
	failed := 0
	for i, r := range results {
		switch {
		case !r.checked:
		case r.err != nil:
			failed++
		case r.posted.After(list[i].Seen):
			fresh = append(fresh, r)
		}
	}
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].posted.After(fresh[j].posted) })

	if len(fresh) == 0 {
		fmt.Fprintln(a.out, "Nothing new since you last looked.")
	} else {
		fmt.Fprintln(a.out, "New since you last looked:")
		for _, r := range fresh {
			fmt.Fprintf(a.out, "  %-32s %s\n", sanitize(r.handle), fmtTime(r.posted.Format(time.RFC3339Nano)))
		}
	}
	if failed > 0 {
		fmt.Fprintf(a.errw, "(%d on your list couldn't be checked right now)\n", failed)
	}
	return nil
}

// latestPost returns when someone last posted or amended a plan entry, or
// the zero time if they have no plan.
func latestPost(user string) (time.Time, error) {
	id, err := lookup(user)
	if err != nil {
		return time.Time{}, err
	}
	plans, err := listPlans(id.PDS, id.DID, 1)
	if err != nil || len(plans) == 0 {
		return time.Time{}, err
	}
	created, _ := time.Parse(time.RFC3339Nano, plans[0].Value.CreatedAt)
	if updated, err := time.Parse(time.RFC3339Nano, plans[0].Value.UpdatedAt); err == nil && updated.After(created) {
		return updated, nil
	}
	return created, nil
}
