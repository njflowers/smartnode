package config

func createMetricsStep(wiz *wizard, currentStep int, totalSteps int) *choiceWizardStep {

	helperText := "Would you like to enable the Smartnode's metrics monitoring system? This will monitor things such as hardware stats (CPU usage, RAM usage, free disk space), your minipool stats, stats about your node such as total RPL and ETH rewards, and much more. It also enables the Grafana dashboard to quickly and easily view these metrics (see https://docs.rocketpool.net/guides/node/grafana.html for an example).\n\nNone of this information will be sent to any remote servers for collection an analysis; this is purely for your own usage on your node."

	show := func(modal *choiceModalLayout) {
		wiz.md.setPage(modal.page)
		if wiz.md.Config.EnableMetrics.Value == true {
			modal.focus(0)
		} else {
			modal.focus(1)
		}
	}

	done := func(buttonIndex int, buttonLabel string) {
		if buttonIndex == 0 {
			wiz.md.Config.EnableMetrics.Value = true
		} else {
			wiz.md.Config.EnableMetrics.Value = false
		}
		wiz.finishedModal.show()
	}

	back := func() {
		if wiz.md.Config.ConsensusClientMode.Value == true {
			wiz.consensusLocalModal.show()
		} else {
			wiz.consensusExternalSelectModal.show()
		}
	}

	return newChoiceStep(
		wiz,
		currentStep,
		totalSteps,
		helperText,
		[]string{"Yes", "No"},
		[]string{},
		76,
		"Metrics",
		DirectionalModalHorizontal,
		show,
		done,
		back,
		"step-metrics",
	)

}
