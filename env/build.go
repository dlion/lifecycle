package env

var BuildEnvAllowlist = []string{
	"CNB_STACK_ID",
	"HOSTNAME",
	"HOME",
}

func NewBuildEnv(environ []string) *Env {
	return &Env{
		RootDirMap: POSIXBuildEnv,
		Vars:       varsFromEnviron(environ, isNotAllowlisted),
	}
}

func isNotAllowlisted(k string) bool {
	for _, wk := range BuildEnvAllowlist {
		if wk == k {
			return false
		}
	}
	for _, wks := range POSIXBuildEnv {
		for _, wk := range wks {
			if wk == k {
				return false
			}
		}
	}
	return true
}

var POSIXBuildEnv = map[string][]string{
	"bin": {
		"PATH",
	},
	"lib": {
		"LD_LIBRARY_PATH",
		"LIBRARY_PATH",
	},
	"include": {
		"CPATH",
	},
	"pkgconfig": {
		"PKG_CONFIG_PATH",
	},
}
