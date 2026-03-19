package postgres

const (
	insertNotification = `
		INSERT INTO notifications (id, batch_id, idempotency_key, recipient, channel, content, priority, status,
			template_id, template_vars, scheduled_at, attempts, max_attempts, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)`

	selectNotificationByID = `
		SELECT id, batch_id, idempotency_key, recipient, channel, content, priority, status,
			template_id, template_vars, scheduled_at, provider_msg_id, attempts, max_attempts,
			last_error, sent_at, created_at, updated_at
		FROM notifications WHERE id = $1`

	selectNotificationsByBatchID = `
		SELECT id, batch_id, idempotency_key, recipient, channel, content, priority, status,
			template_id, template_vars, scheduled_at, provider_msg_id, attempts, max_attempts,
			last_error, sent_at, created_at, updated_at
		FROM notifications WHERE batch_id = $1 ORDER BY created_at LIMIT 1000`

	updateNotificationStatus = `
		UPDATE notifications SET status = $2, updated_at = NOW() WHERE id = $1`

	updateNotificationStatusWithError = `
		UPDATE notifications SET status = $2, last_error = $3, attempts = $4, updated_at = NOW() WHERE id = $1`

	updateNotificationSent = `
		UPDATE notifications SET status = 'sent', provider_msg_id = $2, sent_at = NOW(), updated_at = NOW() WHERE id = $1`

	selectPendingScheduled = `
		SELECT id, batch_id, idempotency_key, recipient, channel, content, priority, status,
			template_id, template_vars, scheduled_at, provider_msg_id, attempts, max_attempts,
			last_error, sent_at, created_at, updated_at
		FROM notifications WHERE status = 'scheduled' AND scheduled_at <= NOW()
		ORDER BY scheduled_at ASC LIMIT $1`

	selectPendingForRecovery = `
		SELECT id, batch_id, idempotency_key, recipient, channel, content, priority, status,
			template_id, template_vars, scheduled_at, provider_msg_id, attempts, max_attempts,
			last_error, sent_at, created_at, updated_at
		FROM notifications WHERE status IN ('pending', 'queued') AND scheduled_at IS NULL
		ORDER BY created_at ASC LIMIT $1`

	insertTemplate = `
		INSERT INTO templates (id, name, channel, content, variables, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`

	selectTemplateByID = `
		SELECT id, name, channel, content, variables, created_at, updated_at
		FROM templates WHERE id = $1`

	selectTemplateByName = `
		SELECT id, name, channel, content, variables, created_at, updated_at
		FROM templates WHERE name = $1`

	updateTemplate = `
		UPDATE templates SET name = $2, channel = $3, content = $4, variables = $5, updated_at = NOW()
		WHERE id = $1`

	deleteTemplate = `DELETE FROM templates WHERE id = $1`

	selectAllTemplates = `
		SELECT id, name, channel, content, variables, created_at, updated_at
		FROM templates ORDER BY name`

	getAndMarkProcessing = `
		UPDATE notifications
		SET status = 'processing', updated_at = NOW()
		WHERE id = $1 AND status IN ('pending', 'queued')
		RETURNING id, batch_id, idempotency_key, recipient, channel, content, priority, status,
			template_id, template_vars, scheduled_at, provider_msg_id, attempts, max_attempts,
			last_error, sent_at, created_at, updated_at`

	updateBatchStatus = `
		UPDATE notifications SET status = $2, updated_at = NOW() WHERE id = ANY($1)`

	getAndMarkProcessingBatch = `
		UPDATE notifications
		SET status = 'processing', updated_at = NOW()
		WHERE id = ANY($1) AND status IN ('pending', 'queued')
		RETURNING id, batch_id, idempotency_key, recipient, channel, content, priority, status,
			template_id, template_vars, scheduled_at, provider_msg_id, attempts, max_attempts,
			last_error, sent_at, created_at, updated_at`

)
