package file

import "net/url"

func ParseBackupURL(backupURL string) (string, string, string, error) {
	u, err := url.Parse(backupURL)
	if err != nil {
		return "", "", "", err
	}
	return u.Scheme, u.Host, u.Path, nil
}
