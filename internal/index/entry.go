package index

type Entry struct {
	Path      string `json:"path"`
	Bucket    string `json:"bucket"`
	DocID     string `json:"doc_id,omitempty"`
	AssetHash string `json:"asset_hash,omitempty"`
	Tombstone bool   `json:"tombstone,omitempty"`
}
