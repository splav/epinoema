package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/splav/epinoema/internal/api"
)

const reportUsage = `использование:
  epi report                       список отчётов
  epi report NAME [-p P] [-tag T]  выполнить; -p и -tag сужают каждую секцию
  epi report save NAME < spec.json сохранить свой отчёт
  epi report rm NAME               удалить свой отчёт`

func runReport(args []string) error {
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()

	if len(args) == 0 {
		reports, err := c.ListReports(ctx)
		if err != nil {
			return err
		}
		for _, r := range reports {
			mark := " "
			if r.Builtin {
				mark = "*"
			}
			fmt.Printf("%s %-12s %s\n", mark, r.Name, describe(r.Spec))
		}
		fmt.Println("\n* встроенные; epi report review — еженедельное ревью")
		return nil
	}

	switch args[0] {
	case "help", "-h", "--help":
		fmt.Println(reportUsage)
		return nil
	case "save":
		if len(args) != 2 {
			return errors.New(reportUsage)
		}
		var spec api.ReportSpec
		dec := json.NewDecoder(os.Stdin)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&spec); err != nil {
			return fmt.Errorf("spec: %w", err)
		}
		created, err := c.SaveReport(ctx, args[1], spec)
		if err != nil {
			return err
		}
		if created {
			fmt.Println("создан:", args[1])
		} else {
			fmt.Println("обновлён:", args[1])
		}
		return nil
	case "rm":
		if len(args) != 2 {
			return errors.New(reportUsage)
		}
		return c.DeleteReport(ctx, args[1])
	}

	name := args[0]
	fs := flag.NewFlagSet("report", flag.ExitOnError)
	project := fs.String("p", "", "только этот проект")
	var tags listFlag
	fs.Var(&tags, "tag", "только с этим тегом; можно повторять")
	fs.Parse(args[1:])

	res, err := c.RunReport(ctx, name, *project, tags)
	if err != nil {
		return err
	}
	now := time.Now()
	for i, sec := range res.Sections {
		if i > 0 {
			fmt.Println()
		}
		if sec.Title != "" {
			fmt.Printf("## %s (%d)\n", sec.Title, len(sec.Notes))
		}
		if len(sec.Notes) == 0 {
			fmt.Println("пусто")
		}
		for _, n := range sec.Notes {
			fmt.Printf("%s  %4s  %-7s %s%s\n", shortID(n.ID), age(now.Sub(n.UpdatedAt)), n.Status, headline(n), suffix(n))
		}
	}
	return nil
}

// suffix — проект и важность в конце строки, если есть.
func suffix(n api.Note) string {
	s := ""
	if n.Project != nil {
		s += "  [" + *n.Project + "]"
	}
	if n.Importance != nil {
		s += fmt.Sprintf("  !%d", *n.Importance)
	}
	return s
}

func describe(spec api.ReportSpec) string {
	secs := spec.Normalized()
	if len(secs) == 1 && secs[0].Title != "" {
		return secs[0].Title
	}
	if len(secs) > 1 {
		s := ""
		for i, sec := range secs {
			if i > 0 {
				s += " + "
			}
			s += sec.Title
		}
		return s
	}
	b, _ := json.Marshal(secs[0].Filter)
	return string(b)
}
