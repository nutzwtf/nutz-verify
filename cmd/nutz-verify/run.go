package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/nutzwtf/nutz-verify/internal/chain"
	"github.com/nutzwtf/nutz-verify/internal/report"
)

const usage = `usage:
  nutz-verify epoch <id> [flags]   recompute and compare one Epoch
  nutz-verify latest [flags]       the most recent posted Root (the Dispute-window default)
  nutz-verify sync [flags]         advance the Cache only

flags:
  --rpc <url>          JSON-RPC endpoint; repeatable, and endpoints that disagree are INDETERMINATE
  --finality <level>   latest, safe or finalized (default safe)
  --rate <n>           JSON-RPC calls per second per endpoint (default 15, the public endpoint's budget)
  --fresh              discard the Cache and rebuild it
  --chain              assert the full Carry chain back to deploy, not just the previous Epoch
  --artifacts <dir>    a published epochs/<id>/ bundle to diff against the Recompute; never an input
  --json               machine-readable output (schema ` + report.SchemaVersion + `)

exit status: 0 MATCH, 1 MISMATCH, 2 INDETERMINATE (could not check; never 0)
`

// options are the flags every command takes.
type options struct {
	rpc       []string
	finality  chain.Finality
	rate      float64
	fresh     bool
	chain     bool
	artifacts string
	json      bool
}

// command is a parsed command line.
type command struct {
	name    string
	epochID uint64 // for epoch
	options
}

// run is the binary: parse, execute, render, and map the Verdict to an exit status. It is
// the whole program minus os.Args and os.Exit, so tests drive it directly.
func run(ctx context.Context, args []string, stdout, stderr io.Writer, dep Deployment) int {
	cmd, err := parse(args)
	if err != nil {
		fmt.Fprintf(stderr, "nutz-verify: %v\n\n%s", err, usage)

		return report.Indeterminate.ExitCode()
	}

	v := &verifier{dep: dep, opts: cmd.options, stderr: stderr}

	rep := report.Report{
		Command: cmd.name,
		Run: report.Run{
			ChainID:        dep.ChainID,
			Distributor:    dep.Distributor.String(),
			Token:          dep.Token.String(),
			DevWallet:      dep.DevWallet.String(),
			Finality:       string(cmd.finality),
			Endpoints:      []string{}, // never null in the JSON, even when the run stops before a Reader exists
			CallsPerSecond: cmd.rate,
		},
	}

	if err := v.execute(ctx, cmd, &rep); err != nil {
		// Whatever stopped the run — an RPC error, an Epoch not yet closed at this
		// finality, endpoints disagreeing, a Cache built for another history — nothing was
		// checked, and "could not check" is INDETERMINATE. Never MATCH (ADR-0002).
		rep.Verdict = report.Indeterminate
		rep.Reason = err.Error()
	}

	if cmd.json {
		if err := report.WriteJSON(stdout, rep); err != nil {
			fmt.Fprintf(stderr, "nutz-verify: writing the report: %v\n", err)

			return report.Indeterminate.ExitCode()
		}
	} else {
		report.Write(stdout, rep)
	}

	if rep.Verdict == "" {
		return 0 // a sync that completed has no Verdict to map
	}

	return rep.Verdict.ExitCode()
}

// parse reads the command line. Flags may come before or after the positional arguments,
// so `epoch 1000 --rpc URL` and `--rpc URL epoch 1000` both work.
func parse(args []string) (command, error) {
	var cmd command

	fs := flag.NewFlagSet("nutz-verify", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	finality := fs.String("finality", string(chain.Safe), "")
	fs.Float64Var(&cmd.rate, "rate", chain.DefaultCallsPerSecond, "")
	fs.BoolVar(&cmd.fresh, "fresh", false, "")
	fs.BoolVar(&cmd.chain, "chain", false, "")
	fs.StringVar(&cmd.artifacts, "artifacts", "", "")
	fs.BoolVar(&cmd.json, "json", false, "")
	fs.Func("rpc", "", func(url string) error {
		cmd.rpc = append(cmd.rpc, url)

		return nil
	})

	var positional []string
	for rest := args; ; {
		if err := fs.Parse(rest); err != nil {
			return cmd, err
		}

		rest = fs.Args()
		if len(rest) == 0 {
			break
		}

		positional = append(positional, rest[0])
		rest = rest[1:]
	}

	if len(positional) == 0 {
		return cmd, errors.New("no command")
	}
	cmd.name = positional[0]

	switch cmd.name {
	case "epoch":
		if len(positional) != 2 {
			return cmd, errors.New("epoch takes exactly one argument, the Epoch id")
		}

		id, err := strconv.ParseUint(positional[1], 10, 64)
		if err != nil {
			return cmd, fmt.Errorf("epoch id %q is not a number", positional[1])
		}
		cmd.epochID = id
	case "latest", "sync":
		if len(positional) != 1 {
			return cmd, fmt.Errorf("%s takes no arguments", cmd.name)
		}
		if cmd.name == "sync" && (cmd.chain || cmd.artifacts != "") {
			return cmd, errors.New("sync recomputes nothing; --chain and --artifacts belong to epoch and latest")
		}
	default:
		return cmd, fmt.Errorf("unknown command %q", cmd.name)
	}

	level, err := chain.ParseFinality(*finality)
	if err != nil {
		return cmd, err
	}
	cmd.finality = level

	if len(cmd.rpc) == 0 {
		return cmd, errors.New("no --rpc endpoint; every input a Recompute has comes from one")
	}
	if cmd.rate <= 0 {
		return cmd, fmt.Errorf("--rate %v is not a rate", cmd.rate)
	}

	return cmd, nil
}

// verifier is one run's state: the pinned Deployment, the flags, and — once the endpoints
// have been reconciled — the Reader and the Distributor's book pinned to that tip.
type verifier struct {
	dep    Deployment
	opts   options
	stderr io.Writer
	reader *chain.Reader
	book   *book
}

// execute runs the command, filling rep as it goes so that a run which fails partway
// still reports everything it established before the failure.
func (v *verifier) execute(ctx context.Context, cmd command, rep *report.Report) error {
	if err := v.dep.check(); err != nil {
		return err
	}

	reader, err := chain.New(chain.Config{
		Endpoints:      v.opts.rpc,
		Token:          v.dep.Token,
		Distributor:    v.dep.Distributor,
		CallsPerSecond: v.opts.rate,
	})
	if err != nil {
		return err
	}
	v.reader = reader
	rep.Run.Endpoints = reader.Endpoints()

	// A stale URL pointing at another chain answers every other question plausibly, and
	// the Cache's header would refuse it only after the reads were made.
	id, err := reader.ChainID(ctx)
	if err != nil {
		return err
	}
	if id != v.dep.ChainID {
		return fmt.Errorf("the endpoints serve chain %d, and this build is pinned to chain %d", id, v.dep.ChainID)
	}

	tip, err := reader.Head(ctx, v.opts.finality)
	if err != nil {
		return err
	}
	rep.Run.Tip = &report.Tip{Number: tip.Block.Number, Hash: tip.Block.Hash.String(), Lag: tip.Lag}
	v.book = newBook(reader, tip.Block.Number)

	switch cmd.name {
	case "sync":
		return v.sync(ctx, tip, rep)
	case "latest":
		latest, err := v.latestEpoch(ctx, tip)
		if err != nil {
			return err
		}

		return v.epoch(ctx, latest, tip, rep)
	default:
		return v.epoch(ctx, cmd.epochID, tip, rep)
	}
}

// progress prints a line to stderr, where it does not disturb --json on stdout.
func (v *verifier) progress(format string, args ...any) {
	fmt.Fprintf(v.stderr, strings.TrimSuffix(format, "\n")+"\n", args...)
}
