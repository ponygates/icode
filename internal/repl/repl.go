package repl

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/ponygates/icode/internal/agent"
	"github.com/ponygates/icode/internal/config"
	"github.com/ponygates/icode/internal/provider"
)

type REPL struct {
	agent    *agent.Agent
	provider provider.Provider
	cfg      *config.Config
}

func New(p provider.Provider, a *agent.Agent, cfg *config.Config) *REPL {
	return &REPL{
		agent:    a,
		provider: p,
		cfg:      cfg,
	}
}

func (r *REPL) Run() error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Printf("iCode> ")

	for {
		input, err := reader.ReadString('\n')
		if err != nil {
			return err
		}

		input = strings.TrimSpace(input)
		if input == "" {
			fmt.Printf("iCode> ")
			continue
		}

		if input == "/quit" || input == "/exit" {
			fmt.Println("bye")
			break
		}

		if input == "/help" {
			fmt.Println("Commands: /quit /exit /help /model <name> /history")
			fmt.Printf("iCode> ")
			continue
		}

		if input == "/history" {
			for _, msg := range r.agent.History() {
				fmt.Printf("[%s] %s\n", msg.Role, msg.Content)
			}
			fmt.Printf("iCode> ")
			continue
		}

		if strings.HasPrefix(input, "/model ") {
			modelName := strings.TrimPrefix(input, "/model ")
			r.cfg.Provider.Default = modelName
			fmt.Printf("Switched to model: %s\n", modelName)
			fmt.Printf("iCode> ")
			continue
		}

		ctx := context.Background()
		if err := r.agent.Run(ctx, input); err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		}

		messages := r.agent.History()
		if len(messages) > 0 {
			last := messages[len(messages)-1]
			if last.Role == "assistant" {
				fmt.Println(last.Content)
			}
		}

		fmt.Printf("iCode> ")
	}

	return nil
}
