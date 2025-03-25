package taponark

type TapClientConfig struct {
	Host      string `yaml:"host"`
	Port      string `yaml:"port"`
	Container string `yaml:"container"`
}

type LndClientConfig struct {
	Host      string `yaml:"host"`
	Port      string `yaml:"port"`
	Container string `yaml:"container"`
}

type BitcoinClientConfig struct {
	Host     string `yaml:"host"`
	Port     string `yaml:"port"`
	User     string `yaml:"user"`
	Password string `yaml:"password"`
}

type Config struct {
	ServerTapClient TapClientConfig `yaml:"server_tap"`
	ServerLndClient LndClientConfig `yaml:"server_lnd"`

	OnboardingUserTapClient TapClientConfig `yaml:"onboard_user_tap"`
	OnboardingUserLndClient LndClientConfig `yaml:"onboard_user_lnd"`

	ExitUserTapClient TapClientConfig `yaml:"exit_user_tap"`
	ExitUserLndClient LndClientConfig `yaml:"exit_user_lnd"`

	BitcoinClient BitcoinClientConfig `yaml:"bitcoin_client"`

	Timeout int64 `yaml:"timeout"`
}
