package doc

type Snapshot struct {
	Version   int            `json:"version"`
	DocID     string         `json:"doc_id"`
	Heads     []OpID         `json:"heads"`
	Value     map[string]any `json:"value"`
	Conflicts []Conflict     `json:"conflicts,omitempty"`
}

func NewSnapshot(doc Document) Snapshot {
	return Snapshot{
		Version:   1,
		DocID:     doc.ID,
		Heads:     append([]OpID(nil), doc.Heads...),
		Value:     doc.Value,
		Conflicts: append([]Conflict(nil), doc.Conflicts...),
	}
}
