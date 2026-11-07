package server_test

import (
	"testing"

	"github.com/splav/epinoema/internal/api"
)

func ptr[T any](v T) *T { return &v }

func TestMe(t *testing.T) {
	e := newEnv(t)
	userID, tok := e.User(t, "alice", "agent")
	var me api.Me
	if code := e.Do(t, "GET", "/v1/me", tok, nil, &me); code != 200 {
		t.Fatalf("me: %d", code)
	}
	if me.UserID != userID || me.Handle != "alice" || me.Actor != "agent" {
		t.Errorf("me: %+v", me)
	}
}

func TestUpdateMeta(t *testing.T) {
	e := newEnv(t)
	_, tok := e.User(t, "alice", "user")
	var n api.Note
	e.Do(t, "POST", "/v1/notes", tok, api.CreateNote{Title: ptr("старое"), Body: "текст", Project: ptr("epi"), Tags: []string{"a"}}, &n)

	var got api.Note
	code := e.Do(t, "PATCH", "/v1/notes/"+n.ID, tok, api.UpdateMeta{Kind: ptr("idea"), Status: ptr("active"), Importance: ptr[int16](3)}, &got)
	if code != 200 {
		t.Fatalf("patch: %d", code)
	}
	if got.Kind != "idea" || got.Status != "active" || *got.Importance != 3 ||
		*got.Title != "старое" || *got.Project != "epi" || len(got.Tags) != 1 || got.Body != "текст" {
		t.Errorf("частичная правка задела лишнее: %+v", got)
	}

	// Пустые значения очищают поля.
	e.Do(t, "PATCH", "/v1/notes/"+n.ID, tok, api.UpdateMeta{Title: ptr(""), Project: ptr(""), Importance: ptr[int16](0), Tags: &[]string{}}, &got)
	if got.Title != nil || got.Project != nil || got.Importance != nil || len(got.Tags) != 0 {
		t.Errorf("очистка: %+v", got)
	}

	if code := e.Do(t, "PATCH", "/v1/notes/"+n.ID, tok, api.UpdateMeta{}, nil); code != 400 {
		t.Errorf("пустой patch: %d, want 400", code)
	}
	if code := e.Do(t, "PATCH", "/v1/notes/"+n.ID, tok, api.UpdateMeta{Status: ptr("archived")}, nil); code != 400 {
		t.Errorf("неизвестный статус: %d, want 400", code)
	}
	if code := e.Do(t, "PATCH", "/v1/notes/"+n.ID, tok, map[string]any{"body": "x"}, nil); code != 400 {
		t.Errorf("правка текста через meta: %d, want 400", code)
	}

	_, bob := e.User(t, "bob", "user")
	if code := e.Do(t, "PATCH", "/v1/notes/"+n.ID, bob, api.UpdateMeta{Status: ptr("done")}, nil); code != 404 {
		t.Errorf("Боб правит чужую заметку: %d, want 404", code)
	}
}

func TestLinks(t *testing.T) {
	e := newEnv(t)
	_, alice := e.User(t, "alice", "user")
	_, bob := e.User(t, "bob", "user")
	var a, b, foreign api.Note
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "a"}, &a)
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "b"}, &b)
	e.Do(t, "POST", "/v1/notes", bob, api.CreateNote{Body: "чужая"}, &foreign)

	link := api.CreateLink{SrcID: b.ID, DstID: a.ID, Kind: "derived_from"}
	if code := e.Do(t, "POST", "/v1/links", alice, link, nil); code != 201 {
		t.Errorf("create: %d, want 201", code)
	}
	if code := e.Do(t, "POST", "/v1/links", alice, link, nil); code != 200 {
		t.Errorf("повтор: %d, want 200", code)
	}

	var detail api.NoteDetail
	e.Do(t, "GET", "/v1/notes/"+a.ID, alice, nil, &detail)
	if len(detail.Links) != 1 || detail.Links[0].SrcID != b.ID || detail.Links[0].Kind != "derived_from" {
		t.Errorf("входящая связь не видна у цели: %+v", detail.Links)
	}

	for name, l := range map[string]api.CreateLink{
		"на чужую заметку": {SrcID: a.ID, DstID: foreign.ID},
		"от чужой заметки": {SrcID: foreign.ID, DstID: a.ID},
		"несуществующая":   {SrcID: a.ID, DstID: "00000000-0000-0000-0000-000000000000"},
	} {
		if code := e.Do(t, "POST", "/v1/links", alice, l, nil); code != 404 {
			t.Errorf("%s: %d, want 404", name, code)
		}
	}
	if code := e.Do(t, "POST", "/v1/links", alice, api.CreateLink{SrcID: a.ID, DstID: a.ID}, nil); code != 400 {
		t.Errorf("сама на себя: %d, want 400", code)
	}
}

func TestSearch(t *testing.T) {
	e := newEnv(t)
	_, alice := e.User(t, "alice", "user")
	_, bob := e.User(t, "bob", "user")
	var ru, en, title api.Note
	var yo api.Note
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "Разобрать идеи про семантический поиск"}, &ru)
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "Спросить ребенка про школу"}, &yo)
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Body: "Try running migrations on startup"}, &en)
	e.Do(t, "POST", "/v1/notes", alice, api.CreateNote{Title: ptr("Embeddings через pgvector"), Body: "потом"}, &title)
	e.Do(t, "POST", "/v1/notes", bob, api.CreateNote{Body: "идея Боба про поиск"}, nil)

	cases := []struct{ q, want string }{
		{"идея", ru.ID},       // snowball давал «иде» и «ид»; hunspell сводит к «идея»
		{"идей", ru.ID},       // родительный падеж множественного числа
		{"поиском", ru.ID},    // падеж
		{"разобрали", ru.ID},  // форма глагола
		{"migration", en.ID},  // английский стемминг в том же конфиге
		{"pgvectr", title.ID}, // опечатка в заголовке
		{"ребёнок", yo.ID},    // ё против е
		{"семантический поиск", ru.ID},
	}
	for _, c := range cases {
		var res api.NoteList
		if code := e.Do(t, "GET", "/v1/search?q="+urlq(c.q), alice, nil, &res); code != 200 {
			t.Fatalf("%q: %d", c.q, code)
		}
		if len(res.Notes) == 0 || res.Notes[0].ID != c.want {
			t.Errorf("%q: первым ожидалась %s, получено %d результатов", c.q, c.want[:8], len(res.Notes))
		}
		for _, n := range res.Notes {
			if n.Body == "идея Боба про поиск" {
				t.Errorf("%q: в выдаче заметка Боба", c.q)
			}
		}
	}
	if code := e.Do(t, "GET", "/v1/search?q=", alice, nil, nil); code != 400 {
		t.Errorf("пустой запрос: %d, want 400", code)
	}
}
