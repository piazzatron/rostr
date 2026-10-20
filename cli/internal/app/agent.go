package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/piazzatron/rostr/cli/internal/project"
	"github.com/piazzatron/rostr/cli/internal/roster"
	"github.com/piazzatron/rostr/cli/internal/store"
)

// draftJSON is what `rostr agent draft --json` prints.
type draftJSON struct {
	Name    string `json:"name"`
	Emoji   string `json:"emoji"`
	Team    string `json:"team"`
	Roster  string `json:"roster"`
	Display string `json:"display"`
}

func newAgentCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Draft, release, and set tasks for agents (run by hooks, the skill, and /draft)",
	}
	cmd.AddCommand(newAgentDraftCommand(), newAgentReleaseCommand(), newAgentTaskCommand())
	return cmd
}

func newAgentDraftCommand() *cobra.Command {
	var task, session, rosterFlag string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "draft",
		Short: "Draft the next free member for this session, A team first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if session == "" {
				return errNoSession
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			d, err := draftSession(ctx, st, session, rosterFlag, workingDir(), task)
			if err != nil {
				return err
			}
			if asJSON {
				return json.NewEncoder(cmd.OutOrStdout()).Encode(draftJSON{
					Name: d.Name(), Emoji: d.Emoji, Team: d.Team, Roster: d.Roster, Display: d.Display(),
				})
			}
			fmt.Fprintln(cmd.OutOrStdout(), d.Display())
			return nil
		},
	}
	cmd.Flags().StringVar(&task, "task", "", "what the agent is working on")
	sessionFlag(cmd, &session)
	cmd.Flags().StringVar(&rosterFlag, "roster", "", "draft from this roster instead of the project's")
	cmd.Flags().BoolVar(&asJSON, "json", false, `print {"name", "emoji", "team", "roster", "display"}`)
	return cmd
}

func newAgentReleaseCommand() *cobra.Command {
	var session, rosterFlag string
	cmd := &cobra.Command{
		Use:   "release [<name>]",
		Short: "Send this session's agent, or a named one, back to the bench",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			out := cmd.OutOrStdout()
			if len(args) == 0 && session == "" {
				return errNoSession
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			// This session's agent
			if len(args) == 0 {
				d, err := st.Release(ctx, session)
				if err != nil {
					return err
				}
				if d == nil {
					fmt.Fprintln(out, "This session has no drafted agent.")
					return nil
				}
				fmt.Fprintf(out, "Released %s from the %s roster.\n", d.Display(), d.Roster)
				return nil
			}

			// A named agent, searched across every roster
			agent, err := findDraftedAgent(ctx, st, args[0], rosterFlag)
			if err != nil {
				return err
			}
			d, err := st.ReleaseAgent(ctx, agent.Key())
			if err != nil {
				return err
			}
			fmt.Fprintf(out, "Released %s from the %s roster.\n", d.Display(), d.Roster)
			return nil
		},
	}
	sessionFlag(cmd, &session)
	cmd.Flags().StringVar(&rosterFlag, "roster", "", "the roster the named agent is in, when more than one has them drafted")
	return cmd
}

func newAgentTaskCommand() *cobra.Command {
	var session, rosterFlag string
	cmd := &cobra.Command{
		Use:   "task [<name>] <description>",
		Short: "Set what an agent is working on",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			if len(args) == 1 && session == "" {
				return errNoSession
			}
			st, err := openStore()
			if err != nil {
				return err
			}
			defer st.Close()

			var agent store.Draft
			if len(args) == 1 {
				d, err := st.SessionDraft(ctx, session)
				if err != nil {
					return err
				}
				if d == nil {
					return fmt.Errorf("this session has no drafted agent. Draft one with /draft")
				}
				agent = *d
			} else {
				if agent, err = findDraftedAgent(ctx, st, args[0], rosterFlag); err != nil {
					return err
				}
			}

			d, err := st.SetTask(ctx, agent.Key(), args[len(args)-1])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s · %s\n", d.Display(), d.Task)
			return nil
		},
	}
	sessionFlag(cmd, &session)
	cmd.Flags().StringVar(&rosterFlag, "roster", "", "the roster the named agent is in, when more than one has them drafted")
	return cmd
}

// draftSession drafts for a session in dir. The store picks the roster for a first draft (--roster, else the
// project's link, else the default), in the same call, so cloud mode drafts in one request.
func draftSession(ctx context.Context, st store.Store, session, rosterFlag, dir, task string) (store.Draft, error) {
	repo, _ := project.Identify(dir)
	return st.Draft(ctx, store.DraftRequest{SessionID: session, Roster: rosterFlag, Repo: repo, Task: task})
}

// findDraftedAgent finds an active draft by name ("Gandalf", "gandalf 2", "eowyn") across every roster, or only in
// rosterFlag's. When several rosters have that name drafted, a person picks one; an agent must pass --roster.
func findDraftedAgent(ctx context.Context, st store.Store, name, rosterFlag string) (store.Draft, error) {
	drafts, err := st.ActiveDrafts(ctx)
	if err != nil {
		return store.Draft{}, err
	}
	var matches []store.Draft
	for _, d := range drafts {
		if roster.NamesMatch(d.Name(), name) && (rosterFlag == "" || roster.NamesMatch(d.Roster, rosterFlag)) {
			matches = append(matches, d)
		}
	}

	switch {
	case len(matches) == 1:
		return matches[0], nil
	case len(matches) == 0 && rosterFlag != "":
		return store.Draft{}, fmt.Errorf("no agent named %q is drafted in the %s roster", name, rosterFlag)
	case len(matches) == 0:
		return store.Draft{}, fmt.Errorf("no agent named %q is drafted", name)
	}

	rosterNames := make([]string, len(matches))
	for i, d := range matches {
		rosterNames[i] = d.Roster
	}
	if !isInteractive() {
		return store.Draft{}, fmt.Errorf("%s is drafted in more than one roster (%s); pass --roster",
			matches[0].Name(), strings.Join(rosterNames, ", "))
	}
	picked, err := promptSelect(fmt.Sprintf("%s is drafted in more than one roster. Which one?", matches[0].Name()), rosterNames)
	if err != nil {
		return store.Draft{}, err
	}
	return matches[picked], nil
}
