package journalLogger

import (
	"github.com/sirupsen/logrus"
	"github.com/ssgreg/journald"
)

//go:generate mockery -name IJournalWriter -inpkg
type IJournalWriter interface {
	Send(msg string, p journald.Priority, fields map[string]interface{}) error
}

type journalHook struct {
	fields map[string]interface{}
	writer IJournalWriter
}

type JournalWriter struct{}

func (*JournalWriter) Send(msg string, p journald.Priority, fields map[string]interface{}) error {
	return journald.Send(msg, p, fields)
}

func NewJournalHook(writer IJournalWriter, fields map[string]interface{}) *journalHook {
	return &journalHook{writer: writer, fields: fields}
}

func (hook *journalHook) getPriority(entry *logrus.Entry) journald.Priority {
	switch entry.Level {
	case logrus.TraceLevel, logrus.DebugLevel:
		return journald.PriorityDebug
	case logrus.InfoLevel:
		return journald.PriorityInfo
	case logrus.WarnLevel:
		return journald.PriorityWarning
	case logrus.ErrorLevel:
		return journald.PriorityErr
	case logrus.FatalLevel:
		return journald.PriorityCrit
	case logrus.PanicLevel:
		return journald.PriorityEmerg
	default:
		return journald.PriorityInfo
	}
}

func (hook *journalHook) Fire(entry *logrus.Entry) error {
	line, err := entry.String()
	if err != nil {
		return err
	}

	return hook.writer.Send(line, hook.getPriority(entry), hook.fields)
}

func (hook *journalHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func SetJournalLogging(logger *logrus.Logger, journalWriter IJournalWriter, fields map[string]interface{}) {
	logger.AddHook(NewJournalHook(journalWriter, fields))
}
