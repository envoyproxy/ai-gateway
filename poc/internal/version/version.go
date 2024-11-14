package version

var gitCommitID string

type Info struct {
	GitCommitID string `json:"gitCommitID"`
}

func (i Info) String() string {
	return gitCommitID
}

func Get() Info {
	return Info{
		GitCommitID: gitCommitID,
	}
}
