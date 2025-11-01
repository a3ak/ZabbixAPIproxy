package logger

import (
	"github.com/a3ak/suffix"
	"github.com/nir0k/logger"
)

type Logging struct {
	FilePath        string   `yaml:"file_path"`
	MaxSize         string   `yaml:"max_size"`
	MaxBackups      int      `yaml:"max_backups"`
	ConsoleLevel    string   `yaml:"console_level"`
	FileLevel       string   `yaml:"file_level"`
	ExcludeRequests []string `yaml:"exclude_requests"`
}

var Global *logger.Logger

func init() {
	Global = &logger.Logger{}
}

func InitLogger(conf Logging) {
	consoleLevel := conf.ConsoleLevel
	if consoleLevel == "" {
		consoleLevel = "error"
	}

	loggerConf := logger.LogConfig{
		FilePath:       conf.FilePath,
		Format:         "standard",
		FileLevel:      conf.FileLevel,
		ConsoleLevel:   consoleLevel,
		ConsoleOutput:  conf.ConsoleLevel != "",
		EnableRotation: true,
		RotationConfig: logger.RotationConfig{
			MaxSize:    int(suffix.UnsafeToMB(conf.MaxSize)),
			MaxBackups: conf.MaxBackups,
			MaxAge:     7,
			Compress:   true,
		},
	}

	if global, err := logger.NewLogger(loggerConf); err != nil {
		panic(err)
	} else {
		Global = global
		global.Infoln("Init LOGGER ", loggerConf)
	}
}
