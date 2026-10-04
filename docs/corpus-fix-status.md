# Read-only Go corpus support

Preserved on a separate branch while runtime bug-fixing work is paused for an incoming upstream PR.

Go's wasip1 os.Open requests broad optional file rights, including write rights in the inheriting mask. Ordinary-file opens negotiate those optional rights down to the parent descriptor. Requested read/write access, create/truncate operations, unknown rights, and directory inheritance remain checked. Granted rights never exceed the parent descriptor; later operations still enforce the granted rights.

The new regression verifies the Go read mask, exact fixture contents, and denied write/truncate attempts. All WASI packages pass on macOS. The initial Linux integration run exposed a directory attenuation regression; directory requests now retain strict inheritance checks. Age's exact output oracle passed on ARM64 and AMD64 during diagnosis.

These changes have not been released and must not be attributed to WASI v0.3.1 or Wago beta.11 measurements.
