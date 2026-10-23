package views

func passwordAutocomplete(registering bool) string {
	if registering {
		return "new-password"
	}
	return "current-password"
}

func authPageTitle(mode string) string {
	if mode == "register" {
		return "Create account"
	}
	return "Sign in"
}
