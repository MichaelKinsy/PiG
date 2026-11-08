package contract

import (
	"github.com/MichaelKinsy/PiG/chord/delta"
	"github.com/MichaelKinsy/PiG/durable"
)

// Session is a catalog row (`Session` of contract.ts): what the sidebar needs without a worker.
type Session struct {
	ID  string
	Cwd string
	// Env is the session's environment, nil for this machine.
	Env           *string
	Title         string
	CreatedAt     int64
	UpdatedAt     int64
	ArchivedAt    *int64
	Pinned        bool
	Model         durable.ModelRef
	ThinkingLevel string
	// Main is the current main conversation, Branches the top-level `/tree` branches.
	Main     int
	Branches []Branch
}

// JSON is the row as `SessionSchema` has it.
func (s Session) JSON() *delta.JsonObject {
	row := delta.NewJsonObject(12)
	row.Set("id", s.ID)
	if s.Env == nil {
		row.Set("env", nil)
	} else {
		row.Set("env", *s.Env)
	}
	row.Set("cwd", s.Cwd)
	row.Set("title", s.Title)
	row.Set("createdAt", float64(s.CreatedAt))
	row.Set("updatedAt", float64(s.UpdatedAt))
	if s.ArchivedAt == nil {
		row.Set("archivedAt", nil)
	} else {
		row.Set("archivedAt", float64(*s.ArchivedAt))
	}
	row.Set("pinned", s.Pinned)
	row.Set("model", delta.JsonObjectOf("provider", s.Model.Provider, "modelId", s.Model.ModelId))
	row.Set("thinkingLevel", s.ThinkingLevel)
	row.Set("main", float64(s.Main))
	branches := make([]any, len(s.Branches))
	for i, branch := range s.Branches {
		branches[i] = branch.JSON()
	}
	row.Set("branches", branches)
	return row
}

// JSON is the branch as `BranchSchema` has it.
func (b Branch) JSON() *delta.JsonObject {
	if b.Label == nil {
		return delta.JsonObjectOf("conversation", float64(b.Conversation), "label", nil)
	}
	return delta.JsonObjectOf("conversation", float64(b.Conversation), "label", *b.Label)
}

// SessionFromJSON reads a row as `SessionSchema` has it (a row the catalog published).
func SessionFromJSON(value any) Session {
	row, _ := value.(*delta.JsonObject)
	if row == nil {
		return Session{}
	}
	s := Session{
		ID:            stringOf(row.Value("id")),
		Cwd:           stringOf(row.Value("cwd")),
		Title:         stringOf(row.Value("title")),
		CreatedAt:     int64(numberOf(row.Value("createdAt"))),
		UpdatedAt:     int64(numberOf(row.Value("updatedAt"))),
		Pinned:        row.Value("pinned") == true,
		ThinkingLevel: stringOf(row.Value("thinkingLevel")),
		Main:          int(numberOf(row.Value("main"))),
	}
	if env, ok := row.Value("env").(string); ok {
		s.Env = &env
	}
	if at, ok := row.Value("archivedAt").(float64); ok {
		archived := int64(at)
		s.ArchivedAt = &archived
	}
	if model, ok := row.Value("model").(*delta.JsonObject); ok {
		s.Model = durable.ModelRef{Provider: stringOf(model.Value("provider")), ModelId: stringOf(model.Value("modelId"))}
	}
	branches, _ := row.Value("branches").([]any)
	for _, each := range branches {
		branch, _ := each.(*delta.JsonObject)
		if branch == nil {
			continue
		}
		entry := Branch{Conversation: int(numberOf(branch.Value("conversation")))}
		if label, ok := branch.Value("label").(string); ok {
			entry.Label = &label
		}
		s.Branches = append(s.Branches, entry)
	}
	return s
}

func numberOf(value any) float64 {
	number, _ := value.(float64)
	return number
}

func stringOf(value any) string {
	text, _ := value.(string)
	return text
}
