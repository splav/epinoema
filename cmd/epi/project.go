package main

import (
	"context"
	"errors"
	"fmt"
	"time"
)

func runProject(args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()

	switch {
	case len(args) == 0:
		projects, err := c.ListProjects(ctx)
		if err != nil {
			return err
		}
		if len(projects) == 0 {
			fmt.Println("проектов нет")
		}
		now := time.Now()
		for _, p := range projects {
			fmt.Printf("%-24s %4d  %s назад\n", p.Name, p.Notes, age(now.Sub(p.LastUpdated)))
		}
		return nil
	case len(args) == 3 && args[0] == "rename":
		res, err := c.RenameProject(ctx, args[1], args[2])
		if err != nil {
			return err
		}
		if res.Merged {
			fmt.Printf("%d заметок перенесено в существующий проект %s\n", res.Renamed, args[2])
		} else {
			fmt.Printf("%s → %s: %d заметок\n", args[1], args[2], res.Renamed)
		}
		return nil
	}
	return errors.New("использование: epi project | epi project rename СТАРОЕ НОВОЕ")
}
