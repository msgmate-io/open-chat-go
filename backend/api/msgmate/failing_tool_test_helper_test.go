package msgmate

// failingTool always fails with err. It is the shared fixture of the tool-loop
// budget tests (ci#688): the point of those tests is which failures consume the
// agent's failed-tool budget, so the tool body itself stays trivial.
type failingTool struct {
	*BaseTool
	err  error
	runs int
}

func (t *failingTool) RunTool(input interface{}) (string, error) {
	t.runs++
	return "", t.err
}
