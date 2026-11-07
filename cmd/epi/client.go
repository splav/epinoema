package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/client"
)

func newClient() (*client.Client, error) {
	tok, err := mustEnv("EPI_TOKEN")
	if err != nil {
		return nil, err
	}
	url := os.Getenv("EPI_URL")
	if url == "" {
		url = "http://127.0.0.1:7777"
	}
	return client.New(strings.TrimRight(url, "/"), tok), nil
}

type listFlag []string

func (l *listFlag) String() string { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error {
	for _, t := range strings.Split(v, ",") {
		if t = strings.TrimSpace(t); t != "" {
			*l = append(*l, t)
		}
	}
	return nil
}

func runAdd(args []string) error {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	title := fs.String("t", "", "заголовок")
	kind := fs.String("k", "", "тип: note, idea, decision, meeting, project")
	project := fs.String("p", "", "проект")
	importance := fs.Int("i", 0, "важность 1–3")
	var tags listFlag
	fs.Var(&tags, "tag", "тег; можно повторять или перечислить через запятую")
	fs.Parse(args)

	body := strings.Join(fs.Args(), " ")
	if body == "" && *title == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return err
		}
		body = string(b)
	}
	body = strings.TrimSpace(body)
	if body == "" && *title == "" {
		return errors.New("пустая заметка")
	}

	req := api.CreateNote{Body: body, Kind: *kind, Tags: tags}
	if *title != "" {
		req.Title = title
	}
	if *project != "" {
		req.Project = project
	}
	if *importance != 0 {
		i := int16(*importance)
		req.Importance = &i
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	note, err := c.CreateNote(context.Background(), req)
	if err != nil {
		return err
	}
	fmt.Println("во входящих:", shortID(note.ID))
	return nil
}

func runInbox(args []string) error {
	fs := flag.NewFlagSet("inbox", flag.ExitOnError)
	n := fs.Int("n", 50, "сколько показать")
	fs.Parse(args)

	c, err := newClient()
	if err != nil {
		return err
	}
	notes, err := c.ListNotes(context.Background(), "inbox", *n)
	if err != nil {
		return err
	}
	if len(notes) == 0 {
		fmt.Println("входящие пусты")
		return nil
	}
	now := time.Now()
	for _, note := range notes {
		fmt.Printf("%s  %4s  %s\n", shortID(note.ID), age(now.Sub(note.CreatedAt)), headline(note))
	}
	return nil
}

func shortID(id string) string { return id[:8] }

// headline — заголовок или первая строка текста, обрезанная до 80 символов.
func headline(n api.Note) string {
	s := n.Body
	if n.Title != nil && *n.Title != "" {
		s = *n.Title
	}
	s, _, _ = strings.Cut(s, "\n")
	if utf8.RuneCountInString(s) > 80 {
		s = string([]rune(s)[:79]) + "…"
	}
	return s
}

func age(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dм", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dч", int(d.Hours()))
	default:
		return fmt.Sprintf("%dд", int(d.Hours()/24))
	}
}
