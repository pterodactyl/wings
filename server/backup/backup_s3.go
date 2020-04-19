package backup

type S3Backup struct {
	// The UUID of this backup object. This must line up with a backup from
	// the panel instance.
	Uuid string

	// An array of files to ignore when generating this backup. This should be
	// compatible with a standard .gitignore structure.
	IgnoredFiles []string
}

var _ Backup = (*S3Backup)(nil)

func (s *S3Backup) Identifier() string {
	return s.Uuid
}

func (s *S3Backup) Backup(included *IncludedFiles, prefix string) error {
	panic("implement me")
}

func (s *S3Backup) Checksum() ([]byte, error) {
	return []byte(""), nil
}

func (s *S3Backup) Size() (int64, error) {
	return 0, nil
}

func (s *S3Backup) Path() string {
	return ""
}

func (s *S3Backup) Details() *ArchiveDetails {
	return &ArchiveDetails{}
}
