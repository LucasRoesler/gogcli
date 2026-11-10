// Command gog-inbox is a terminal view of today's agenda and the Gmail inbox.
// It reads Google through gog in-process, so it uses gog's accounts and auth.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/fake"
	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/gog"
	"github.com/openclaw/gogcli/contrib/gog-inbox/internal/ui"
)

func main() {
	dump := flag.String("dump", "", "print one frame at `WxH` (e.g. 90x22) as plain text and exit")
	keys := flag.String("keys", "", "with --dump, comma-separated key presses to apply first (e.g. enter or tab,j,enter)")
	layout := flag.String("layout", "stacked", "pane `layout`: stacked (agenda over inbox when the pane is tall enough) or tabs (one list at a time); v toggles at runtime")
	fixtures := flag.Bool("fixtures", false, "show built-in sample data instead of reading Google")
	account := flag.String("account", "", "gog account email or alias (default: gog's default account)")
	maxThreads := flag.Int("max", 50, "inbox threads to load")
	noOpen := flag.Bool("no-open", false, "show links in the status line instead of running xdg-open")
	refresh := flag.Duration("refresh", ui.DefaultRefresh, "reload the agenda and inbox this often (e.g. 30s, 5m); 0 reloads only on r")

	flag.Parse()

	if err := ui.CheckRefresh(*refresh); err != nil {
		fmt.Fprintln(os.Stderr, "gog-inbox:", err)
		os.Exit(2)
	}

	lay, err := ui.ParseLayout(*layout)
	if err != nil {
		fmt.Fprintln(os.Stderr, "gog-inbox:", err)
		os.Exit(2)
	}

	now := time.Now()

	opts := ui.Options{Now: time.Now, Layout: lay, Open: xdgOpen, Refresh: *refresh}
	if *fixtures {
		opts.Events, opts.Mails = fake.Agenda(now), fake.Inbox(now)
	} else {
		opts.Source = gog.Source{Account: *account, Max: *maxThreads, Run: gog.InProcess}
	}

	if *noOpen {
		opts.Open = nil
	}

	if *dump != "" {
		if opts.Source != nil {
			// A dump is one frame, so load synchronously first.
			if err := preload(&opts, now); err != nil {
				fmt.Fprintln(os.Stderr, "gog-inbox:", err)
				os.Exit(1)
			}

			opts.Preloaded = true
		}

		m := ui.New(opts)

		var w, h int
		if _, err := fmt.Sscanf(*dump, "%dx%d", &w, &h); err != nil {
			fmt.Fprintf(os.Stderr, "gog-inbox: --dump wants WxH, got %q\n", *dump)
			os.Exit(2)
		}

		m = m.SetSize(w, h)
		if *keys != "" {
			m = m.Press(strings.Split(*keys, ",")...)
		}

		fmt.Println(strings.Join(m.Frame(), "\n"))

		return
	}

	if _, err := tea.NewProgram(ui.New(opts)).Run(); err != nil {
		fmt.Fprintln(os.Stderr, "gog-inbox:", err)
		os.Exit(1)
	}
}

func preload(opts *ui.Options, now time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	var err error
	if opts.Events, err = opts.Source.Agenda(ctx, now); err != nil {
		return fmt.Errorf("agenda: %w", err)
	}

	if opts.Mails, err = opts.Source.Inbox(ctx); err != nil {
		return fmt.Errorf("inbox: %w", err)
	}

	return nil
}

func xdgOpen(url string) error {
	cmd := exec.CommandContext(context.Background(), "xdg-open", url) //nolint:gosec // one argv entry, no shell; the URL comes from the user's own calendar or mail.

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("xdg-open: %w", err)
	}

	go func() { _ = cmd.Wait() }()

	return nil
}
