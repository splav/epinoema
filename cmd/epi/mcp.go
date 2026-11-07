package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/splav/epinoema/internal/mcpserver"
)

// runMCP запускает stdio MCP-сервер. stdout занят протоколом, поэтому всё
// остальное пишется только в stderr.
func runMCP(args []string) error {
	flag.NewFlagSet("mcp", flag.ExitOnError).Parse(args)
	c, err := newClient()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	me, err := c.Me(ctx)
	if err != nil {
		return fmt.Errorf("API недоступен: %w", err)
	}
	// С токеном человека правки агента в истории неотличимы от ваших.
	if me.Actor != "agent" {
		return fmt.Errorf("токен выпущен для %q, а агенту нужен свой: epi token create -user %s -actor agent -name claude-code", me.Actor, me.Handle)
	}
	return mcpserver.New(c).Run(ctx, &mcp.StdioTransport{})
}
