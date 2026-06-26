package validate

func validationInfoLogger(logger Logger, explicit InfoLogger) InfoLogger {
	if explicit != nil {
		return explicit
	}
	info, ok := logger.(InfoLogger)
	if !ok {
		return nil
	}
	return info
}
