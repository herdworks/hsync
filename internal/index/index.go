package index

import "sort"

const SiteIndexDocID = "site-index"

type Index struct {
	Entries map[string]Entry `json:"entries"`
}

func New() Index {
	return Index{Entries: map[string]Entry{}}
}

func (idx Index) List() []Entry {
	entries := make([]Entry, 0, len(idx.Entries))
	for _, entry := range idx.Entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Path < entries[j].Path
	})
	return entries
}

func (idx *Index) Set(entry Entry) {
	if idx.Entries == nil {
		idx.Entries = map[string]Entry{}
	}
	idx.Entries[entry.Path] = entry
}

func (idx Index) Get(path string) (Entry, bool) {
	entry, ok := idx.Entries[path]
	return entry, ok
}
