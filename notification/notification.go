package notification

import (
	"time"

	"github.com/exmonitor/exclient/database"
	dbnotification "github.com/exmonitor/exclient/database/spec/notification"
	"github.com/exmonitor/exlogger"
	"github.com/pkg/errors"

	"github.com/exmonitor/firefly/notification/email"
	"github.com/exmonitor/firefly/notification/phone"
	"github.com/exmonitor/firefly/notification/sms"
	"github.com/exmonitor/firefly/service/state"
)

type Config struct {
	ServiceID                  int
	Failed                     bool
	NotificationSentTimestamps map[int]time.Time
	NotificationChangeChannel  chan state.NotificationChange

	DBClient database.ClientInterface
	Logger   *exlogger.Logger
}

const (
	contactTypeEmail = "email"
	contactTypeSms   = "sms"
	contactTypePhone = "phone"
)

func New(conf Config) (*Service, error) {
	if conf.ServiceID <= 0 {
		return nil, errors.Wrap(invalidConfigError, "conf.DBCLient must be positive number")
	}
	if conf.DBClient == nil {
		return nil, errors.Wrap(invalidConfigError, "conf.DBCLient must not be nil")
	}
	if conf.Logger == nil {
		return nil, errors.Wrap(invalidConfigError, "conf.Logger must not be nil")
	}

	newService := &Service{
		checkId:                   conf.ServiceID,
		failed:                    conf.Failed,
		notificationSentTimestamp: conf.NotificationSentTimestamps,
		notificationChangeChannel: conf.NotificationChangeChannel,

		dbClient: conf.DBClient,
		logger:   conf.Logger,
	}

	return newService, nil
}

type Service struct {
	checkId                   int
	failed                    bool
	notificationSentTimestamp map[int]time.Time
	notificationChangeChannel chan state.NotificationChange

	dbClient database.ClientInterface
	logger   *exlogger.Logger
}

// for goroutine
func (s *Service) Run() {
	// fetch all user notification settings
	notificationSettings, err := s.dbClient.SQL_GetUsersNotificationSettings(s.checkId)
	if err != nil {
		s.logger.LogError(err, "failed to fetch user notification settings")
	}

	// get monitoring service details
	serviceInfo, err := s.dbClient.SQL_GetServiceDetails(s.checkId)
	if err != nil {
		s.logger.LogError(err, "failed to fetch service info")
	}

	for _, n := range notificationSettings {
		// check if we should resent notification
		if !s.canSentNotification(n) {
			// notification was already sent and its still to early to resent
			continue
		}
		switch n.Type {
		case contactTypeEmail:
			msg := EmailTemplate(s.failed, serviceInfo)
			err := email.Send(n.Target, msg)
			if err != nil {
				s.logger.LogError(err, "failed to send Email to %s for check id %d", n.Target, s.checkId)
			}
			break
		case contactTypeSms:
			msg := SMSTemplate(s.failed, serviceInfo)
			err := sms.Send(n.Target, msg)
			if err != nil {
				s.logger.LogError(err, "failed to send SMS to %s for check id %d", n.Target, s.checkId)
			}
			break
		case contactTypePhone:
			msg := CallTemplate(s.failed, serviceInfo)
			err := phone.Call(n.Target, msg)
			if err != nil {
				s.logger.LogError(err, "failed to call to %s for check id %d", n.Target, s.checkId)
			}
		}
	}
}

// functo to determine if notification should be sent
func (s *Service) canSentNotification(notificationSettings *dbnotification.UserNotificationSettings) bool {
	if notifTimestamp, ok := s.notificationSentTimestamp[notificationSettings.ID]; ok {
		// there is already record so this means notification was at sent at least once
		// let check if its time to resent
		if notificationSettings.ResentSettings == 1 {
			// notification settings 0 means dont resent notification ever
			return false
		}
		resentAfter := s.getResentDuration(notificationSettings.ResentSettings)

		// checking if resent interval passed since last notification
		if time.Now().After(notifTimestamp.Add(resentAfter)) {
			nc := state.NotificationChange{
				ServiceID:      s.checkId,
				NotificationID: notificationSettings.ID,
			}
			s.notificationChangeChannel <- nc
			// sent notification
			s.logger.LogDebug("resending notification for serviceID %d, notificationID %d after %.0fm", s.checkId, notificationSettings.ID, resentAfter.Minutes())
			return true
		} else {
			// interval for resending has not elapsed, dont sent notification
			return false
		}
	} else {
		// there is no record in notificationSentTimeStamp for this notify id, so should sent first notification
		nc := state.NotificationChange{
			ServiceID:      s.checkId,
			NotificationID: notificationSettings.ID,
		}
		s.notificationChangeChannel <- nc
		// sent notification
		s.logger.LogDebug("send first notification for serviceID %d, notificationID %d", s.checkId, notificationSettings.ID)
		return true
	}

}

func (s *Service) getResentDuration(resentSettings int) time.Duration {
	var resentAfter time.Duration

	switch resentSettings {
	case 2:
		resentAfter = time.Minute * 10
		break
	case 3:
		resentAfter = time.Minute * 20
		break
	case 4:
		resentAfter = time.Minute * 30
		break
	case 5:
		resentAfter = time.Minute * 60
		break
	case 6:
		resentAfter = time.Minute * 120
		break
	case 7:
		resentAfter = time.Minute * 240
		break
	default:
		s.logger.LogError(nil, "unknown resentSettings id %d, using default 240min", resentSettings)
		resentAfter = time.Minute * 240
	}

	return resentAfter
}
