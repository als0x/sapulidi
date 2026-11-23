package cli

type Command struct {
	Name    string
	Summary string
	Run     func(app *App, args []string) int // returns the exit code
}

var commands = map[string]Command{}

func Register(c Command) { commands[c.Name] = c }
