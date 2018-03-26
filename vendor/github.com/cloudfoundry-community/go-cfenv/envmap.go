package cfenv

import "strings"

// splitEnv splits item, a key=value string, into its key and value components.
func splitEnv(item string) (key, val string) {
	splits := strings.Split(item, "=")
	key = splits[0]
	val = strings.Join(splits[1:], "=")
	return
}

func mapEnv(data []string, keyFunc func(item string) (key, val string)) map[string]string {
	items := make(map[string]string)
	for _, item := range data {
		key, val := keyFunc(item)
		items[key] = val
	}
	return items
}
