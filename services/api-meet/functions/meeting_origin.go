package functions

// isAgentMeeting reconoce el origen actual y las reuniones históricas de Inbox.
func isAgentMeeting(event calendarEvent) bool {
	return event.ExtendedProperties.Private["agent"] == "true" || event.ExtendedProperties.Shared["createdBy"] == "indomito-inbox-agent"
}
