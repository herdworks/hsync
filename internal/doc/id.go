package doc

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

type OpID struct {
	Actor string `json:"actor"`
	Seq   uint64 `json:"seq"`
}

func (id OpID) String() string {
	return id.Actor + ":" + strconv.FormatUint(id.Seq, 10)
}

func ParseOpID(s string) (OpID, error) {
	actor, seqText, ok := strings.Cut(s, ":")
	if !ok || actor == "" || seqText == "" {
		return OpID{}, fmt.Errorf("invalid op id %q", s)
	}
	seq, err := strconv.ParseUint(seqText, 10, 64)
	if err != nil || seq == 0 {
		return OpID{}, fmt.Errorf("invalid op id sequence %q", s)
	}
	return OpID{Actor: actor, Seq: seq}, nil
}

func CompareOpID(a, b OpID) int {
	if a.Seq < b.Seq {
		return -1
	}
	if a.Seq > b.Seq {
		return 1
	}
	if a.Actor < b.Actor {
		return -1
	}
	if a.Actor > b.Actor {
		return 1
	}
	return 0
}

func SortOpIDs(ids []OpID) {
	sort.Slice(ids, func(i, j int) bool {
		return CompareOpID(ids[i], ids[j]) < 0
	})
}
