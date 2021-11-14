package conf

import (
	"encoding/json"
	"io/ioutil"
	"os"

	"gopkg.in/yaml.v2"
)

var Config Configuration

type ServerSMTP struct {
	Host    string `yaml:"host"`
	Source  string `yaml:"source"`
	Pass    string `yaml:"pass"`
	Timeout uint32 `yaml:"timeout"`
	Count   uint16 `yaml:"count"`
}

type Queue struct {
	Host  string `yaml:"host"`
	Login string `yaml:"login"`
	Pass  string `yaml:"pass"`
}

type Configuration struct {
	IP           string       `yaml:"ip"`
	ReadTimeout  uint32       `yaml:"timeout"`
	CertPath     string       `yaml:"cert"`
	KeyPath      string       `yaml:"key"`
	SMTP         []ServerSMTP `yaml:"smtp"`
	Q            Queue        `yaml"queue"`
	ChannelEmail string       `yaml:"channelEmail"`
	WorkersName  string       `yaml:"workersName"`
}

func ReadConfig(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	confData, err := ioutil.ReadAll(f)
	if err != nil {
		return err
	}
	err = json.Unmarshal(confData, &Config)
	if err != nil {
		err = yaml.Unmarshal(confData, &Config)
		if err != nil {
			return err
		}
	}
	return nil
}
