package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// LoadDataset reads a JSON dataset from path. The file's shape is
// exactly the Dataset struct, so a hand-edited file and a JSON-
// marshalled Go value round-trip without conversion.
func LoadDataset(path string) (Dataset, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- caller-supplied dataset path; datasets are non-secret fixtures by design
	if err != nil {
		return Dataset{}, fmt.Errorf("eval: read %s: %w", path, err)
	}
	var d Dataset
	if err := json.Unmarshal(raw, &d); err != nil {
		return Dataset{}, fmt.Errorf("eval: decode %s: %w", path, err)
	}
	if err := validateDataset(d); err != nil {
		return Dataset{}, fmt.Errorf("eval: %s: %w", path, err)
	}
	return d, nil
}

// MustLoadDataset is the panicking variant of LoadDataset. Convenient
// at process startup; do not use in long-running services where a
// missing file should be a recoverable error.
func MustLoadDataset(path string) Dataset {
	d, err := LoadDataset(path)
	if err != nil {
		panic(err)
	}
	return d
}

// SaveDataset writes d to path as indented JSON. Useful when a
// dataset is generated programmatically (e.g., sampled from a trace
// store) and persisted for future regression runs.
func SaveDataset(d Dataset, path string) error {
	if err := validateDataset(d); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644) // #nosec G306 -- 0644 is correct; datasets are repo-committed fixtures, not secrets
}

func validateDataset(d Dataset) error {
	if d.Name == "" {
		return errors.New("Dataset.Name is empty")
	}
	if d.Version == "" {
		return errors.New("Dataset.Version is empty")
	}
	if len(d.Cases) == 0 {
		return errors.New("Dataset.Cases is empty")
	}
	seen := make(map[string]struct{}, len(d.Cases))
	for i, c := range d.Cases {
		if c.ID == "" {
			return fmt.Errorf("Cases[%d].ID is empty", i)
		}
		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("duplicate Case.ID %q", c.ID)
		}
		seen[c.ID] = struct{}{}
	}
	return nil
}
