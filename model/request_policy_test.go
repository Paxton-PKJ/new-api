package model

import (
	"errors"
	"maps"
	"math"
	"os"
	"strconv"
	"testing"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/setting/operation_setting"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

func TestRequestPolicyDatabaseMatrix(t *testing.T) {
	for _, dialect := range []string{"sqlite", "mysql", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			var driver gorm.Dialector
			switch dialect {
			case "sqlite":
				driver = sqlite.Open(":memory:")
			case "mysql":
				dsn := os.Getenv("TEST_MYSQL_DSN")
				if dsn == "" {
					t.Skip("TEST_MYSQL_DSN not configured")
				}
				driver = mysql.Open(dsn)
			case "postgres":
				dsn := os.Getenv("TEST_POSTGRES_DSN")
				if dsn == "" {
					t.Skip("TEST_POSTGRES_DSN not configured")
				}
				driver = postgres.Open(dsn)
			}
			db, err := gorm.Open(driver, &gorm.Config{NamingStrategy: schema.NamingStrategy{TablePrefix: "policy_test_"}})
			require.NoError(t, err)
			sqlDB, err := db.DB()
			require.NoError(t, err)
			sqlDB.SetMaxOpenConns(1)
			previousDB, previousType, previousSnapshot := DB, common.MainDatabaseType(), CurrentRequestPolicy()
			common.OptionMapRWMutex.Lock()
			previousOptions := maps.Clone(common.OptionMap)
			common.OptionMap = maps.Clone(previousSnapshot.Options)
			common.OptionMapRWMutex.Unlock()
			DB = db
			common.SetMainDatabaseType(common.DatabaseType(dialect))
			initCol()
			t.Cleanup(func() {
				require.NoError(t, db.Migrator().DropTable(&Option{}))
				require.NoError(t, db.Migrator().DropTable(&Token{}))
				for k, v := range previousSnapshot.Options {
					require.NoError(t, updateOptionMap(k, v))
				}
				requestPolicySnapshot.Store(previousSnapshot)
				common.OptionMapRWMutex.Lock()
				common.OptionMap = previousOptions
				common.OptionMapRWMutex.Unlock()
				DB = previousDB
				common.SetMainDatabaseType(previousType)
				initCol()
				require.NoError(t, sqlDB.Close())
			})
			require.NoError(t, db.AutoMigrate(&Option{}))
			var version string
			query := "SELECT version()"
			if dialect == "sqlite" {
				query = "SELECT sqlite_version()"
			}
			require.NoError(t, db.Raw(query).Scan(&version).Error)
			t.Logf("database version: %s", version)
			rules := `[{"name":"session","model_regex":[".*"],"key_sources":[{"type":"request_header","key":"X-Session"}],"ttl_seconds":0,"skip_retry_on_failure":false,"include_using_group":false,"param_override_template":{"temperature":0},"future_field":{"enabled":false}}]`
			require.NoError(t, UpdateRequestPolicyOptions(map[string]string{"RetryTimes": "2", "channel_affinity_setting.rules": rules}))
			assert.Equal(t, 2, CurrentRequestPolicy().RetryTimes)
			assert.Equal(t, 2, common.RetryTimes, "the runtime global follows the same write")
			require.NoError(t, UpdateOption("AutomaticRetryStatusCodes", "429,500-503"))
			assert.Equal(t, "429,500-503", CurrentRequestPolicy().Options["AutomaticRetryStatusCodes"])
			assert.Equal(t, "429,500-503", operation_setting.AutomaticRetryStatusCodesToString())
			assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"request_policy_setting.mode": "off"}), "the removed mode switch is not a policy option")
			assert.False(t, CurrentRequestPolicy().Affinity.Rules[0].SkipRetryOnFailure)
			assert.Equal(t, rules, CurrentRequestPolicy().Options["channel_affinity_setting.rules"])
			loadOptionsFromDatabase()
			loadOptionsFromDatabase()
			assert.Equal(t, rules, CurrentRequestPolicy().Options["channel_affinity_setting.rules"], "legacy rule JSON survives reloads without dropping extension fields")
			require.NoError(t, UpdateRequestPolicyOptions(map[string]string{"channel_affinity_setting.session_mode": "strict"}))
			loadOptionsFromDatabase()
			loadOptionsFromDatabase()
			assert.Equal(t, "strict", CurrentRequestPolicy().Affinity.SessionMode)
			assert.Equal(t, rules, CurrentRequestPolicy().Options["channel_affinity_setting.rules"], "a global mode never rewrites rule fields")
			require.NoError(t, UpdateRequestPolicyOptions(map[string]string{"DefaultSameChannelRetryTimes": "2", "MaxTotalAttempts": "8"}))
			assert.Equal(t, 2, CurrentRequestPolicy().DefaultSameChannelRetryTimes)
			assert.Equal(t, 2, common.DefaultSameChannelRetryTimes, "the runtime global follows the same write")
			assert.Equal(t, 8, CurrentRequestPolicy().MaxTotalAttempts)
			assert.Equal(t, 8, common.MaxTotalAttempts, "the runtime global follows the same write")
			loadOptionsFromDatabase()
			loadOptionsFromDatabase()
			assert.Equal(t, 2, CurrentRequestPolicy().DefaultSameChannelRetryTimes, "the attempt budget survives reloads")
			assert.Equal(t, 8, CurrentRequestPolicy().MaxTotalAttempts, "the attempt budget survives reloads")
			snapshot := CurrentRequestPolicy()
			assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"channel_affinity_setting.session_mode": "unknown"}))
			assert.Same(t, snapshot, CurrentRequestPolicy())
			for _, value := range []string{"-1", "1.5", "bad", strconv.Itoa(math.MaxInt)} {
				assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"RetryTimes": value}))
				assert.Same(t, snapshot, CurrentRequestPolicy())
			}
			for _, value := range []string{"11", "-1", "x"} {
				assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"DefaultSameChannelRetryTimes": value}))
				assert.Same(t, snapshot, CurrentRequestPolicy())
			}
			for _, value := range []string{"1000", "-1", "x"} {
				assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"MaxTotalAttempts": value}))
				assert.Same(t, snapshot, CurrentRequestPolicy())
			}
			assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"channel_affinity_setting.rules": "[", "RetryTimes": "8"}))
			assert.Same(t, snapshot, CurrentRequestPolicy())
			var option Option
			require.NoError(t, db.Where(map[string]any{"key": "RetryTimes"}).First(&option).Error)
			assert.Equal(t, "2", option.Value)
			require.NoError(t, db.Callback().Update().Before("gorm:update").Register("policy_fail", func(tx *gorm.DB) {
				if tx.Statement.Table == "policy_test_options" {
					tx.AddError(errors.New("save rejected"))
				}
			}))
			assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"RetryTimes": "8", "channel_affinity_setting.session_mode": "off"}))
			assert.Same(t, snapshot, CurrentRequestPolicy())
			require.NoError(t, db.Callback().Update().Remove("policy_fail"))
			option = Option{}
			require.NoError(t, db.Where(map[string]any{"key": "RetryTimes"}).First(&option).Error)
			assert.Equal(t, "2", option.Value)
			loadOptionsFromDatabase()
			assert.Equal(t, "strict", CurrentRequestPolicy().Affinity.SessionMode, "a failed save keeps the persisted global mode")

			// 直接渠道路由：开关默认关闭、渠道数量有范围，关闭时若仍有令牌在用则整体拒绝。
			directSnapshot := CurrentRequestPolicy()
			assert.False(t, directSnapshot.EnableDirectChannelRouting)
			assert.Equal(t, 10, directSnapshot.MaxRoutePresetChannels)
			for _, value := range []string{"0", "65", "x"} {
				assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"MaxRoutePresetChannels": value}))
			}
			assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"EnableDirectChannelRouting": "yes"}))
			assert.Same(t, directSnapshot, CurrentRequestPolicy(), "rejected direct routing options keep the snapshot")
			require.NoError(t, UpdateRequestPolicyOptions(map[string]string{"EnableDirectChannelRouting": "true", "MaxRoutePresetChannels": "5"}))
			assert.True(t, common.EnableDirectChannelRouting, "the runtime global follows the same write")
			assert.Equal(t, 5, common.MaxRoutePresetChannels)

			require.NoError(t, db.AutoMigrate(&Token{}))
			directToken := Token{UserId: 7, Key: "direct-guard-key", Name: "direct-guard", Status: common.TokenStatusEnabled, ExpiredTime: -1, UnlimitedQuota: true}
			require.NoError(t, directToken.SetProfileConfig(&TokenProfileConfig{
				ActiveProfile: "p",
				Profiles: []TokenProfile{{
					Name:              "p",
					ActiveRoutePreset: "direct",
					RoutePresets:      []TokenRoutePreset{{Name: "direct", RouteKeys: []string{"ch_0123456789AbCdEf"}}},
				}},
			}))
			require.NoError(t, directToken.Insert())
			err = UpdateRequestPolicyOptions(map[string]string{"EnableDirectChannelRouting": "false"})
			require.Error(t, err)
			assert.EqualError(t, err, "direct channel routing is still used by 1 token(s); switch their active route presets first")
			assert.True(t, common.EnableDirectChannelRouting, "a rejected disable keeps the runtime global on")
			assert.Error(t, UpdateRequestPolicyOptions(map[string]string{"EnableDirectChannelRouting": "False"}),
				"the guard follows the parsed value, not the request literal")
			assert.True(t, common.EnableDirectChannelRouting)

			require.NoError(t, directToken.SetProfileConfig(nil))
			require.NoError(t, directToken.Update())
			require.NoError(t, UpdateRequestPolicyOptions(map[string]string{"EnableDirectChannelRouting": "false", "MaxRoutePresetChannels": "10"}))
			assert.False(t, common.EnableDirectChannelRouting, "the switch turns off once no token uses it")
		})
	}
}
