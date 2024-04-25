/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package logger

import (
	"flag"
	"github.com/natefinch/lumberjack"
	"io/ioutil"
	"log"
	"os"
	"path"
	"path/filepath"
)

import (
	"github.com/apache/dubbo-getty"

	perrors "github.com/pkg/errors"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"gopkg.in/yaml.v2"
)

import (
	"github.com/apache/dubbo-go/common/constant"
)

var (
	logger Logger
)

// nolint
type DubboLogger struct {
	Logger
	dynamicLevel zap.AtomicLevel
}

// Logger is the interface for Logger types
type Logger interface {
	Info(args ...interface{})
	Warn(args ...interface{})
	Error(args ...interface{})
	Debug(args ...interface{})
	Fatal(args ...interface{})

	Infof(fmt string, args ...interface{})
	Warnf(fmt string, args ...interface{})
	Errorf(fmt string, args ...interface{})
	Debugf(fmt string, args ...interface{})
	Fatalf(fmt string, args ...interface{})
}

func init() {
	// forbidden to executing twice.
	if logger != nil {
		return
	}

	fs := flag.NewFlagSet("log", flag.ContinueOnError)
	logConfFile := fs.String("logConf", os.Getenv(constant.APP_LOG_CONF_FILE), "default log config path")
	fs.Parse(os.Args[1:])
	for len(fs.Args()) != 0 {
		fs.Parse(fs.Args()[1:])
	}
	err := InitLog(*logConfFile)
	if err != nil {
		log.Printf("[InitLog] warn: %v", err)
	}
}

// InitLog use for init logger by call InitLogger
func InitLog(logConfFile string) error {
	if logConfFile == "" {
		InitLoggerWithRolling(nil, nil)
		return perrors.New("log configure file name is nil")
	}
	if path.Ext(logConfFile) != ".yml" {
		InitLoggerWithRolling(nil, nil)
		return perrors.Errorf("log configure file name{%s} suffix must be .yml", logConfFile)
	}

	confFileStream, err := ioutil.ReadFile(logConfFile)
	if err != nil {
		InitLoggerWithRolling(nil, nil)
		return perrors.Errorf("ioutil.ReadFile(file:%s) = error:%v", logConfFile, err)
	}

	logConfig := &ConfigWrapper{
		LogConfig: zap.Config{},
		Rolling:   RollingFileConfig{},
	}

	err = yaml.Unmarshal(confFileStream, logConfig)
	if err != nil {
		InitLoggerWithRolling(nil, nil)
		return perrors.Errorf("yaml.Unmarshal(file:%s) = error:%v", logConfFile, err)
	}

	InitLoggerWithRolling(&logConfig.LogConfig, &logConfig.Rolling)

	return nil
}

func InitLoggerWithRolling(conf *zap.Config, rolling *RollingFileConfig) {

	var zapLoggerConfig zap.Config
	if conf == nil {
		zapLoggerEncoderConfig := zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			CallerKey:      "caller",
			MessageKey:     "message",
			StacktraceKey:  "stacktrace",
			EncodeLevel:    zapcore.CapitalLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.SecondsDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		}
		zapLoggerConfig = zap.Config{
			Level:            zap.NewAtomicLevelAt(zap.DebugLevel),
			Development:      false,
			Encoding:         "console",
			EncoderConfig:    zapLoggerEncoderConfig,
			OutputPaths:      []string{"stderr"},
			ErrorOutputPaths: []string{"stderr"},
		}
	} else {
		zapLoggerConfig = *conf
	}

	var lumberjackRolling RollingFileConfig

	if rolling == nil {
		lumberjackRolling = RollingFileConfig{
			LogFilePath:   "./logs",
			ErrorFilename: "dubbo-error.log",
			WarnFilename:  "dubbo-warn.log",
			InfoFilename:  "dubbo-info.log",
			MaxSize:       30,
			MaxBackups:    1,
			MaxAge:        3,
			Compress:      false,
		}
	} else {
		lumberjackRolling = *rolling
	}

	logEncoder := zapcore.NewJSONEncoder(zapLoggerConfig.EncoderConfig)

	infoLogger := initLumberjackLogger(lumberjackRolling.InfoFilename, lumberjackRolling)
	infoLevel := zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		return level == zapcore.InfoLevel && level-zapcore.InfoLevel-zapLoggerConfig.Level.Level() > -1
	})
	warnLogger := initLumberjackLogger(lumberjackRolling.WarnFilename, lumberjackRolling)
	warnLevel := zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		return level == zapcore.WarnLevel && zapcore.WarnLevel-zapLoggerConfig.Level.Level() > -1
	})
	errorLogger := initLumberjackLogger(lumberjackRolling.ErrorFilename, lumberjackRolling)
	errorLevel := zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		return level > zapcore.WarnLevel && zapcore.WarnLevel-zapLoggerConfig.Level.Level() > -1
	})

	consoleLevel := zap.LevelEnablerFunc(func(level zapcore.Level) bool {
		return level-zapLoggerConfig.Level.Level() > -1
	})

	zapCores := []zapcore.Core{
		zapcore.NewCore(logEncoder, zapcore.AddSync(infoLogger), infoLevel),
		zapcore.NewCore(logEncoder, zapcore.AddSync(warnLogger), warnLevel),
		zapcore.NewCore(logEncoder, zapcore.AddSync(errorLogger), errorLevel),
		zapcore.NewCore(logEncoder, zapcore.AddSync(os.Stdout), consoleLevel),
	}

	zapLogger, _ := zapLoggerConfig.Build(
		zap.AddCallerSkip(1),
		zap.WrapCore(func(core zapcore.Core) zapcore.Core {
			return zapcore.NewTee(zapCores...)
		}),
	)

	logger = &DubboLogger{Logger: zapLogger.Sugar(), dynamicLevel: zapLoggerConfig.Level}
	// set getty log
	getty.SetLogger(logger)
}

// InitLogger use for init logger by @conf
func InitLogger(conf *zap.Config) {
	InitLoggerWithRolling(conf, nil)
}

// SetLogger sets logger for dubbo and getty
func SetLogger(log Logger) {
	logger = log
	getty.SetLogger(logger)
}

// GetLogger gets the logger
func GetLogger() Logger {
	return logger
}

// SetLoggerLevel use for set logger level
func SetLoggerLevel(level string) bool {
	if l, ok := logger.(OpsLogger); ok {
		l.SetLoggerLevel(level)
		return true
	}
	return false
}

// OpsLogger use for the SetLoggerLevel
type OpsLogger interface {
	Logger
	SetLoggerLevel(level string)
}

// SetLoggerLevel use for set logger level
func (dl *DubboLogger) SetLoggerLevel(level string) {
	l := new(zapcore.Level)
	if err := l.Set(level); err == nil {
		dl.dynamicLevel.SetLevel(*l)
	}
}

func initLumberjackLogger(filename string, fileConfig RollingFileConfig) *lumberjack.Logger {
	// 创建info级别的lumberjack logger实例
	lumberjackLogger := &lumberjack.Logger{
		Filename:   fileConfig.LogFilePath + string(filepath.Separator) + filename,
		MaxSize:    fileConfig.MaxSize,
		MaxBackups: fileConfig.MaxBackups,
		MaxAge:     fileConfig.MaxAge,
		Compress:   fileConfig.Compress,
	}
	return lumberjackLogger
}

type RollingFileConfig struct {
	LogFilePath   string `json:"logFilePath" yaml:"logFilePath"`     // 日志路径
	ErrorFilename string `json:"errorFilename" yaml:"errorFilename"` // 默认名称：error.log
	WarnFilename  string `json:"warnFilename" yaml:"warnFilename"`   // 默认名称：warn.log
	InfoFilename  string `json:"infoFilename" yaml:"infoFilename"`   // 默认名称：info.log
	MaxSize       int    `json:"maxSize" yaml:"maxSize"`             // 一个文件多少Ｍ（大于该数字开始切分文件）
	MaxBackups    int    `json:"maxBackups" yaml:"maxBackups"`       // MaxBackups是要保留的最大旧日志文件数
	MaxAge        int    `json:"maxAge" yaml:"maxAge"`               // MaxAge是根据日期保留旧日志文件的最大天数
	Compress      bool   `json:"compress" yaml:"compress"`           // 是否压缩
}

type ConfigWrapper struct {
	LogConfig zap.Config        `json:"logConfig" yaml:"logConfig"`
	Rolling   RollingFileConfig `json:"rolling" yaml:"rolling"`
}
