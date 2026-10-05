//go:build windows || (darwin && go1.27)

package p2

type directoryReadState struct{}

func readDirectoryEntry(d *directoryStream, _ uint64) (string, uint32, error) {
	entries, err := d.file.ReadDir(1)
	if err != nil || len(entries) == 0 {
		return "", 0, err
	}
	entry := entries[0]
	return entry.Name(), descriptorModeKind(entry.Type()), nil
}

func closeDirectoryStream(d *directoryStream) error { return d.file.Close() }
