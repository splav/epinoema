package main

import (
	"context"
	"errors"
	"flag"
	"fmt"

	"github.com/splav/epinoema/internal/api"
	"github.com/splav/epinoema/internal/client"
)

// resolve превращает короткий id из вывода CLI в полный.
func resolve(ctx context.Context, c *client.Client, id string) (api.NoteDetail, error) {
	n, err := c.GetNote(ctx, id)
	if err != nil {
		return n, fmt.Errorf("%s: %w", id, err)
	}
	return n, nil
}

func runLink(args []string) error {
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	kind := fs.String("k", "related", "related, derived_from, supersedes, depends_on (A начинается после B)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return errors.New("использование: epi link [-k depends_on] A B")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	src, err := resolve(ctx, c, fs.Arg(0))
	if err != nil {
		return err
	}
	dst, err := resolve(ctx, c, fs.Arg(1))
	if err != nil {
		return err
	}
	created, err := c.CreateLink(ctx, api.CreateLink{SrcID: src.ID, DstID: dst.ID, Kind: *kind})
	if err != nil {
		return err
	}
	verb := map[string]string{"depends_on": "после", "derived_from": "выросла из", "supersedes": "заменяет"}[*kind]
	if verb == "" {
		verb = "связана с"
	}
	msg := fmt.Sprintf("«%s» %s «%s»", headline(src.Note), verb, headline(dst.Note))
	if !created {
		msg += " (уже было)"
	}
	fmt.Println(msg)
	return nil
}

// runSet меняет метаданные. Флаг с пустым значением очищает поле: -due "".
func runSet(args []string) error {
	if len(args) == 0 {
		return errors.New("использование: epi set ID [-start D] [-due D] [-status S] [-k K] [-p P] [-i N] [-t T] [-tag a,b]")
	}
	id, args := args[0], args[1:]
	fs := flag.NewFlagSet("set", flag.ExitOnError)
	start := fs.String("start", "", "дата начала YYYY-MM-DD")
	due := fs.String("due", "", "срок YYYY-MM-DD")
	status := fs.String("status", "", "inbox, active, parked, done, dropped")
	kind := fs.String("k", "", "note, idea, decision, meeting, project")
	project := fs.String("p", "", "проект")
	title := fs.String("t", "", "заголовок")
	importance := fs.Int("i", 0, "важность 1–3, 0 — убрать")
	var tags listFlag
	fs.Var(&tags, "tag", "полный список тегов через запятую")
	fs.Parse(args)

	var m api.UpdateMeta
	fs.Visit(func(f *flag.Flag) {
		switch f.Name {
		case "start":
			m.StartOn = start
		case "due":
			m.DueOn = due
		case "status":
			m.Status = status
		case "k":
			m.Kind = kind
		case "p":
			m.Project = project
		case "t":
			m.Title = title
		case "i":
			i := int16(*importance)
			m.Importance = &i
		case "tag":
			t := []string(tags)
			m.Tags = &t
		}
	})
	if m.Empty() {
		return errors.New("нечего менять: укажите хотя бы один флаг")
	}
	c, err := newClient()
	if err != nil {
		return err
	}
	ctx := context.Background()
	n, err := resolve(ctx, c, id)
	if err != nil {
		return err
	}
	if _, err := c.UpdateMeta(ctx, n.ID, m); err != nil {
		return err
	}
	fmt.Println("обновлено:", shortID(n.ID), headline(n.Note))
	return nil
}
