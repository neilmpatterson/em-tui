package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/neilmpatterson/em-tui/internal/config"
	"github.com/neilmpatterson/em-tui/internal/jira"
	"github.com/neilmpatterson/em-tui/internal/wizard"
)

type Screen int

const (
	ScreenWizard Screen = iota
	ScreenMainMenu
	ScreenStandup
	ScreenOneOnOne
	ScreenSprintReport
	ScreenPerfReview
	ScreenStats
)

// NavigateTo switches the active screen.
type NavigateTo struct {
	Screen Screen
	// teamIdx for team-scoped screens; memberPayload for member screens.
	Payload any
}

// GoBack returns to the main menu without recreating it, preserving team/person selection.
type GoBack struct{}

// memberPayload carries both team and member indices for member-scoped screens.
type memberPayload struct{ team, member int }

// deleteTeamMsg removes a team from config by index.
type deleteTeamMsg int

// deleteMemberMsg removes a member from a team.
type deleteMemberMsg struct{ team, member int }

// openWizardMsg re-opens the wizard (e.g. from "Modify team").
type openWizardMsg struct{}

type App struct {
	screen     Screen
	cfg        *config.Config
	cfgPath    string
	jiraClient *jira.Client

	wizard     wizard.Model
	mainMenu   MainMenuModel
	standup    StandupModel
	oneOnOne   OneOnOneModel
	sprintRpt  SprintReportModel
	perfReview PerfReviewModel
	statsView  StatsModel

	width  int
	height int
}

func New(cfg *config.Config, cfgPath string, forceWizard bool) App {
	startScreen := ScreenMainMenu
	if config.IsSetupRequired(cfg) || forceWizard {
		startScreen = ScreenWizard
	}

	var jiraClient *jira.Client
	if !config.IsSetupRequired(cfg) {
		if c, err := jira.New(cfg.Jira.BaseURL, cfg.Jira.Email, cfg.Jira.APIToken); err == nil {
			jiraClient = c
		}
	}

	return App{
		screen:     startScreen,
		cfg:        cfg,
		cfgPath:    cfgPath,
		jiraClient: jiraClient,
		wizard:     wizard.New(cfg), // always seed with existing config
		mainMenu:   NewMainMenu(cfg),
	}
}

func (a App) Init() tea.Cmd {
	if a.screen == ScreenWizard {
		return a.wizard.Init()
	}
	return a.mainMenu.Init()
}

func (a App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		a.width, a.height = msg.Width, msg.Height
		return a.delegate(msg)

	case tea.KeyMsg:
		if msg.String() == "ctrl+c" {
			return a, tea.Quit
		}

	case GoBack:
		a.screen = ScreenMainMenu
		return a, nil

	case NavigateTo:
		return a.handleNavigate(msg)

	case openWizardMsg:
		a.wizard = wizard.New(a.cfg)
		a.screen = ScreenWizard
		return a, a.wizard.Init()

	case wizard.DoneMsg:
		a.cfg = msg.Config
		a.cfgPath = msg.ConfigPath
		if c, err := jira.New(a.cfg.Jira.BaseURL, a.cfg.Jira.Email, a.cfg.Jira.APIToken); err == nil {
			a.jiraClient = c
		}
		a.screen = ScreenMainMenu
		a.mainMenu = NewMainMenu(a.cfg)
		return a, a.mainMenu.Init()

	case deleteTeamMsg:
		idx := int(msg)
		if idx >= 0 && idx < len(a.cfg.Teams) {
			a.cfg.Teams = append(a.cfg.Teams[:idx], a.cfg.Teams[idx+1:]...)
			_ = config.Save(a.cfg, a.cfgPath)
		}
		a.screen = ScreenMainMenu
		a.mainMenu = NewMainMenu(a.cfg)
		return a, a.mainMenu.Init()

	case deleteMemberMsg:
		t, m := msg.team, msg.member
		if t < len(a.cfg.Teams) && m < len(a.cfg.Teams[t].Members) {
			a.cfg.Teams[t].Members = append(a.cfg.Teams[t].Members[:m], a.cfg.Teams[t].Members[m+1:]...)
			_ = config.Save(a.cfg, a.cfgPath)
		}
		a.screen = ScreenMainMenu
		a.mainMenu = NewMainMenu(a.cfg)
		return a, a.mainMenu.Init()
	}

	return a.delegate(msg)
}

func (a App) handleNavigate(msg NavigateTo) (tea.Model, tea.Cmd) {
	a.screen = msg.Screen
	switch msg.Screen {
	case ScreenMainMenu:
		a.mainMenu = NewMainMenu(a.cfg)
		return a, a.mainMenu.Init()
	case ScreenStandup:
		teamIdx, _ := msg.Payload.(int)
		a.standup = NewStandup(a.cfg, teamIdx, a.jiraClient, a.width, a.height)
		return a, a.standup.Init()
	case ScreenOneOnOne:
		p, _ := msg.Payload.(memberPayload)
		a.oneOnOne = NewOneOnOne(a.cfg, p.team, a.jiraClient, p.member, a.width, a.height)
		return a, a.oneOnOne.Init()
	case ScreenSprintReport:
		teamIdx, _ := msg.Payload.(int)
		a.sprintRpt = NewSprintReport(a.cfg, teamIdx, a.jiraClient, a.width, a.height)
		return a, a.sprintRpt.Init()
	case ScreenPerfReview:
		p, _ := msg.Payload.(memberPayload)
		a.perfReview = NewPerfReview(a.cfg, p.team, a.jiraClient, p.member)
		return a, a.perfReview.Init()
	case ScreenStats:
		p, _ := msg.Payload.(memberPayload)
		a.statsView = NewStats(a.cfg, p.team, a.jiraClient, p.member, a.width, a.height)
		return a, a.statsView.Init()
	case ScreenWizard:
		a.wizard = wizard.New(a.cfg)
		return a, a.wizard.Init()
	}
	return a, nil
}

func (a App) delegate(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch a.screen {
	case ScreenWizard:
		var m tea.Model
		m, cmd = a.wizard.Update(msg)
		a.wizard = m.(wizard.Model)
	case ScreenMainMenu:
		var m tea.Model
		m, cmd = a.mainMenu.Update(msg)
		a.mainMenu = m.(MainMenuModel)
	case ScreenStandup:
		var m tea.Model
		m, cmd = a.standup.Update(msg)
		a.standup = m.(StandupModel)
	case ScreenOneOnOne:
		var m tea.Model
		m, cmd = a.oneOnOne.Update(msg)
		a.oneOnOne = m.(OneOnOneModel)
	case ScreenSprintReport:
		var m tea.Model
		m, cmd = a.sprintRpt.Update(msg)
		a.sprintRpt = m.(SprintReportModel)
	case ScreenPerfReview:
		var m tea.Model
		m, cmd = a.perfReview.Update(msg)
		a.perfReview = m.(PerfReviewModel)
	case ScreenStats:
		var m tea.Model
		m, cmd = a.statsView.Update(msg)
		a.statsView = m.(StatsModel)
	}
	return a, cmd
}

func (a App) View() string {
	var body string
	switch a.screen {
	case ScreenWizard:
		body = a.wizard.View()
	case ScreenMainMenu:
		body = a.mainMenu.View()
	case ScreenStandup:
		body = a.standup.View()
	case ScreenOneOnOne:
		body = a.oneOnOne.View()
	case ScreenSprintReport:
		body = a.sprintRpt.View()
	case ScreenPerfReview:
		body = a.perfReview.View()
	case ScreenStats:
		body = a.statsView.View()
	default:
		body = fmt.Sprintf("unknown screen %d", a.screen)
	}
	footer := lipgloss.NewStyle().Faint(true).Render("ctrl+c quit")
	return lipgloss.JoinVertical(lipgloss.Left, body, "", footer)
}
