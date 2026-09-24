package configformat

import "testing"

type releasedConfig struct {
	Server struct {
		Port int `json:"port"`
	} `json:"server"`
}

func TestReleasedReadersDecodeSupportedFormats(t *testing.T) {
	fixtures := []struct {
		path string
		data string
	}{
		{path: "config.json", data: `{"server":{"port":37421}}`},
		{path: "config.yaml", data: "server:\n  port: 37421\n"},
		{path: "config.toml", data: "[server]\nport = 37421\n"},
	}
	for _, fixture := range fixtures {
		t.Run(fixture.path, func(t *testing.T) {
			format, err := Detect(fixture.path)
			if err != nil {
				t.Fatal(err)
			}
			var decoded releasedConfig
			if err := Unmarshal(format, []byte(fixture.data), &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Server.Port != 37421 {
				t.Fatalf("port=%d", decoded.Server.Port)
			}
		})
	}
}
