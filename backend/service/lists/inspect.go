package lists

import (
	"errors"
	"io/fs"
	"os"

	"github.com/shiinobu/hecatedns/backend/lib/config"
)

const (
	StateOK       = "ok"
	StateDisabled = "disabled"
	StateMissing  = "missing"
	StateCorrupt  = "corrupt"
)

func Inspect(dir string, src config.Source) (string, Meta) {
	if !src.Enabled {
		return StateDisabled, Meta{}
	}
	if !validListName(src.Name) {
		return StateCorrupt, Meta{}
	}
	if _, err := os.Stat(listPath(dir, src.Name)); errors.Is(err, fs.ErrNotExist) {
		return StateMissing, Meta{}
	} else if err != nil {
		return StateCorrupt, Meta{}
	}
	meta, err := ReadMeta(dir, src.Name)
	if err != nil || meta.Count <= 0 {
		return StateCorrupt, Meta{}
	}
	return StateOK, meta
}
