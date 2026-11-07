package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/splav/epinoema/internal/gantt"
)

func runGantt(args []string) error {
	fs := flag.NewFlagSet("gantt", flag.ExitOnError)
	project := fs.String("p", "", "только этот проект")
	var tags listFlag
	fs.Var(&tags, "tag", "только с этим тегом; можно повторять")
	mermaid := fs.Bool("mermaid", false, "вывести Mermaid вместо полос")
	width := fs.Int("w", 100, "ширина в символах")
	fs.Parse(args)

	c, err := newClient()
	if err != nil {
		return err
	}
	g, err := c.Gantt(context.Background(), *project, tags)
	if err != nil {
		return err
	}
	if *mermaid {
		fmt.Print(g.Mermaid)
	} else {
		fmt.Printf("## %s\n", g.Title)
		fmt.Print(gantt.Text(g.Bars, *width))
	}
	for _, w := range g.Warnings {
		fmt.Fprintln(os.Stderr, "⚠", w)
	}
	return nil
}
