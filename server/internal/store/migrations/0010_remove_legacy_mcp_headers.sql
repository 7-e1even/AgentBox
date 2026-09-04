-- The legacy textarea accepted unrestricted header values. Preserve only
-- complete env/secret references, convert them to the canonical array, and
-- record (without copying the value) when an unsafe value had to be removed.
CREATE TEMP TABLE migration_0010_unsafe_mcp_url_ids (
  id TEXT PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO migration_0010_unsafe_mcp_url_ids(id)
SELECT resource.id
FROM control_resources resource
WHERE resource.kind = 'mcp'
  AND lower(btrim(COALESCE(resource.spec->>'transport', ''))) = 'http'
  AND resource.spec->'url' IS NOT NULL
  AND (
    jsonb_typeof(resource.spec->'url') <> 'string'
    OR btrim(resource.spec->>'url') !~ $url$^https?://[^[:space:]/?#@]+(/[^[:space:]?#]*)?$$url$
  );

CREATE TEMP TABLE migration_0010_affected_mcp_ids (
  id TEXT PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO migration_0010_affected_mcp_ids(id)
SELECT resource.id
FROM control_resources resource
WHERE resource.kind = 'mcp' AND (
  EXISTS (
    SELECT 1 FROM migration_0010_unsafe_mcp_url_ids unsafe_url
    WHERE unsafe_url.id = resource.id
  )
  OR (resource.spec ? 'headers' AND (
    jsonb_typeof(resource.spec->'headers') <> 'array'
    OR EXISTS (
      SELECT 1
      FROM jsonb_array_elements(
        CASE WHEN jsonb_typeof(resource.spec->'headers') = 'array'
          THEN resource.spec->'headers' ELSE '[]'::jsonb END
      ) AS header(value)
      WHERE jsonb_typeof(header.value) <> 'object'
        OR (CASE WHEN jsonb_typeof(header.value) = 'object'
              THEN header.value ELSE '{}'::jsonb END - 'name' - 'valueFrom') <> '{}'::jsonb
        OR COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
              THEN header.value->>'name' END, '') !~ $header$^[A-Za-z0-9!#$%&'*+.^_`|~-]+$$header$
        OR COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
              THEN header.value->>'valueFrom' END, '') !~ $reference$^(env|secret)://[A-Za-z_][A-Za-z0-9_]*$$reference$
        OR lower(COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
              THEN header.value->>'name' END, '')) IN (
          'accept', 'content-type', 'connection', 'host', 'content-length',
          'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te',
          'trailer', 'transfer-encoding', 'upgrade', 'mcp-protocol-version',
          'mcp-session-id', 'last-event-id', 'mcp-method', 'mcp-name'
        )
    )
    OR EXISTS (
      SELECT 1
      FROM jsonb_array_elements(
        CASE WHEN jsonb_typeof(resource.spec->'headers') = 'array'
          THEN resource.spec->'headers' ELSE '[]'::jsonb END
      ) AS header(value)
      GROUP BY lower(COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
        THEN header.value->>'name' END, ''))
      HAVING count(*) > 1
    )
  ))
);

CREATE TEMP TABLE migration_0010_affected_sandbox_ids (
  id TEXT PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO migration_0010_affected_sandbox_ids(id)
SELECT DISTINCT job.resource_id
FROM worker_jobs job
WHERE job.resource_id IS NOT NULL
  AND EXISTS (
    SELECT 1
    FROM jsonb_array_elements(
      CASE WHEN jsonb_typeof(job.payload->'mcpServers') = 'array'
        THEN job.payload->'mcpServers' ELSE '[]'::jsonb END
    ) AS definition(value)
    WHERE (
      lower(btrim(COALESCE(definition.value #>> '{spec,transport}', ''))) = 'http'
      AND definition.value #> '{spec,url}' IS NOT NULL
      AND (
        jsonb_typeof(definition.value #> '{spec,url}') <> 'string'
        OR btrim(definition.value #>> '{spec,url}') !~ $url$^https?://[^[:space:]/?#@]+(/[^[:space:]?#]*)?$$url$
      )
    ) OR (
      definition.value #> '{spec,headers}' IS NOT NULL
      AND (
        jsonb_typeof(definition.value #> '{spec,headers}') <> 'array'
        OR EXISTS (
          SELECT 1
          FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(definition.value #> '{spec,headers}') = 'array'
              THEN definition.value #> '{spec,headers}' ELSE '[]'::jsonb END
          ) AS header(value)
          WHERE jsonb_typeof(header.value) <> 'object'
            OR (CASE WHEN jsonb_typeof(header.value) = 'object'
                  THEN header.value ELSE '{}'::jsonb END - 'name' - 'valueFrom') <> '{}'::jsonb
            OR COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
                  THEN header.value->>'name' END, '') !~ $header$^[A-Za-z0-9!#$%&'*+.^_`|~-]+$$header$
            OR COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
                  THEN header.value->>'valueFrom' END, '') !~ $reference$^(env|secret)://[A-Za-z_][A-Za-z0-9_]*$$reference$
            OR lower(COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
                  THEN header.value->>'name' END, '')) IN (
              'accept', 'content-type', 'connection', 'host', 'content-length',
              'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te',
              'trailer', 'transfer-encoding', 'upgrade', 'mcp-protocol-version',
              'mcp-session-id', 'last-event-id', 'mcp-method', 'mcp-name'
            )
        )
        OR EXISTS (
          SELECT 1
          FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(definition.value #> '{spec,headers}') = 'array'
              THEN definition.value #> '{spec,headers}' ELSE '[]'::jsonb END
          ) AS header(value)
          GROUP BY lower(COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
            THEN header.value->>'name' END, ''))
          HAVING count(*) > 1
        )
      )
    )
  );

INSERT INTO system_logs(level, category, action, message, resource_kind, resource_id, status, detail)
SELECT 'warn', 'resource', 'mcp-url-removed',
  'An unsafe legacy MCP URL was removed and the resource was disabled during migration',
  'mcp', unsafe.id, 'success',
  '{"migration":"0010_remove_legacy_mcp_headers"}'::jsonb
FROM migration_0010_unsafe_mcp_url_ids unsafe;

UPDATE control_resources resource
SET spec = resource.spec - 'url', enabled = FALSE, updated_at = NOW()
FROM migration_0010_unsafe_mcp_url_ids unsafe
WHERE resource.id = unsafe.id;

WITH unsafe AS (
  SELECT resource.id
  FROM control_resources resource
	WHERE resource.kind = 'mcp'
	  AND jsonb_typeof(resource.spec->'headers') = 'string'
	  AND btrim(resource.spec->>'headers') <> ''
	  AND (
	    EXISTS (
	      SELECT 1
	      FROM regexp_split_to_table(replace(resource.spec->>'headers', E'\r\n', E'\n'), E'\n') AS header(line)
	      WHERE header.line !~ $header$^[[:space:]]*[A-Za-z0-9!#$%&'*+.^_`|~-]+[[:space:]]*=[[:space:]]*(env|secret)://[A-Za-z_][A-Za-z0-9_]*[[:space:]]*$$header$
	        OR lower(btrim(split_part(header.line, '=', 1))) IN (
            'accept', 'content-type', 'connection', 'host', 'content-length',
            'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te',
            'trailer', 'transfer-encoding', 'upgrade', 'mcp-protocol-version',
            'mcp-session-id', 'last-event-id', 'mcp-method', 'mcp-name'
	          )
	      )
	    OR EXISTS (
	      SELECT 1
	      FROM regexp_split_to_table(replace(resource.spec->>'headers', E'\r\n', E'\n'), E'\n') AS header(line)
	      GROUP BY lower(btrim(split_part(header.line, '=', 1)))
	      HAVING count(*) > 1
	    )
	  )
)
INSERT INTO system_logs(level, category, action, message, resource_kind, resource_id, status, detail)
SELECT 'warn', 'resource', 'mcp-headers-removed',
  'Legacy MCP headers contained a literal or invalid value and were removed during migration',
  'mcp', unsafe.id, 'success', '{"migration":"0010_remove_legacy_mcp_headers"}'::jsonb
FROM unsafe;

WITH safe AS (
  SELECT resource.id, (
    SELECT jsonb_agg(jsonb_build_object(
	      'name', btrim(split_part(header.line, '=', 1)),
	      'valueFrom', btrim(substring(header.line FROM position('=' IN header.line) + 1))
    ) ORDER BY header.ordinality)
    FROM regexp_split_to_table(replace(resource.spec->>'headers', E'\r\n', E'\n'), E'\n')
      WITH ORDINALITY AS header(line, ordinality)
  ) AS headers
  FROM control_resources resource
  WHERE resource.kind = 'mcp'
    AND jsonb_typeof(resource.spec->'headers') = 'string'
    AND btrim(resource.spec->>'headers') <> ''
    AND NOT EXISTS (
      SELECT 1
      FROM regexp_split_to_table(replace(resource.spec->>'headers', E'\r\n', E'\n'), E'\n') AS header(line)
	    WHERE header.line !~ $header$^[[:space:]]*[A-Za-z0-9!#$%&'*+.^_`|~-]+[[:space:]]*=[[:space:]]*(env|secret)://[A-Za-z_][A-Za-z0-9_]*[[:space:]]*$$header$
	      OR lower(btrim(split_part(header.line, '=', 1))) IN (
          'accept', 'content-type', 'connection', 'host', 'content-length',
          'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te',
          'trailer', 'transfer-encoding', 'upgrade', 'mcp-protocol-version',
          'mcp-session-id', 'last-event-id', 'mcp-method', 'mcp-name'
	      )
	  )
	  AND NOT EXISTS (
	    SELECT 1
	    FROM regexp_split_to_table(replace(resource.spec->>'headers', E'\r\n', E'\n'), E'\n') AS header(line)
	    GROUP BY lower(btrim(split_part(header.line, '=', 1)))
	    HAVING count(*) > 1
	  )
)
UPDATE control_resources resource
SET spec = jsonb_set(resource.spec, '{headers}', safe.headers, TRUE)
FROM safe
WHERE resource.id = safe.id;

UPDATE control_resources
SET spec = spec - 'headers'
WHERE kind = 'mcp' AND jsonb_typeof(spec->'headers') = 'string';

-- Defensive cleanup for any manually inserted pre-canonical shape. Current
-- API writes can only persist a validated array of {name,valueFrom} objects.
-- Keep an audit marker without ever copying the removed values.
WITH unsafe AS (
  SELECT resource.id
  FROM control_resources resource
  WHERE resource.kind = 'mcp'
    AND jsonb_typeof(resource.spec->'headers') = 'array'
    AND (
    EXISTS (
      SELECT 1
      FROM jsonb_array_elements(
        CASE WHEN jsonb_typeof(resource.spec->'headers') = 'array'
          THEN resource.spec->'headers' ELSE '[]'::jsonb END
      ) AS header(value)
      WHERE jsonb_typeof(header.value) <> 'object'
        OR (CASE WHEN jsonb_typeof(header.value) = 'object'
              THEN header.value ELSE '{}'::jsonb END - 'name' - 'valueFrom') <> '{}'::jsonb
        OR COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
              THEN header.value->>'name' END, '') !~ $header$^[A-Za-z0-9!#$%&'*+.^_`|~-]+$$header$
        OR COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
              THEN header.value->>'valueFrom' END, '') !~ $reference$^(env|secret)://[A-Za-z_][A-Za-z0-9_]*$$reference$
        OR lower(COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
              THEN header.value->>'name' END, '')) IN (
          'accept', 'content-type', 'connection', 'host', 'content-length',
          'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te',
          'trailer', 'transfer-encoding', 'upgrade', 'mcp-protocol-version',
          'mcp-session-id', 'last-event-id', 'mcp-method', 'mcp-name'
        )
    )
    OR EXISTS (
      SELECT 1
      FROM jsonb_array_elements(
        CASE WHEN jsonb_typeof(resource.spec->'headers') = 'array'
          THEN resource.spec->'headers' ELSE '[]'::jsonb END
      ) AS header(value)
      GROUP BY lower(COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
        THEN header.value->>'name' END, ''))
      HAVING count(*) > 1
    )
    )
), removed AS (
  UPDATE control_resources resource
  SET spec = resource.spec - 'headers'
  FROM unsafe
  WHERE resource.id = unsafe.id
  RETURNING resource.id
)
INSERT INTO system_logs(level, category, action, message, resource_kind, resource_id, status, detail)
SELECT 'warn', 'resource', 'mcp-headers-removed',
  'Malformed canonical MCP headers were removed during migration',
  'mcp', removed.id, 'success',
  '{"migration":"0010_remove_legacy_mcp_headers","shape":"array"}'::jsonb
FROM removed;

WITH unsafe AS (
  SELECT id, COALESCE(jsonb_typeof(spec->'headers'), 'unknown') AS shape
  FROM control_resources
  WHERE kind = 'mcp'
    AND spec ? 'headers'
    AND jsonb_typeof(spec->'headers') <> 'array'
), removed AS (
  UPDATE control_resources resource
  SET spec = resource.spec - 'headers'
  FROM unsafe
  WHERE resource.id = unsafe.id
  RETURNING resource.id, unsafe.shape
)
INSERT INTO system_logs(level, category, action, message, resource_kind, resource_id, status, detail)
SELECT 'warn', 'resource', 'mcp-headers-removed',
  'Unsupported MCP headers shape was removed during migration',
  'mcp', removed.id, 'success', jsonb_build_object(
    'migration', '0010_remove_legacy_mcp_headers', 'shape', removed.shape
  )
FROM removed;

-- MCP definitions are copied into Worker job payloads. Rewrite safe references
-- and remove every literal or malformed value in pending, leased, and terminal
-- jobs so a historical payload cannot retain or later reintroduce plaintext.
WITH definitions AS (
  SELECT job.id AS job_id, definition.ordinality, definition.value AS definition,
    definition.value #> '{spec,headers}' AS headers,
    definition.value #> '{spec,url}' AS url,
    lower(btrim(COALESCE(definition.value #>> '{spec,transport}', ''))) AS transport
  FROM worker_jobs job
  CROSS JOIN LATERAL jsonb_array_elements(
    CASE WHEN jsonb_typeof(job.payload->'mcpServers') = 'array'
      THEN job.payload->'mcpServers' ELSE '[]'::jsonb END
  ) WITH ORDINALITY AS definition(value, ordinality)
), classified AS (
  SELECT definitions.*,
    CASE
      WHEN transport = 'http' AND url IS NOT NULL AND (
        jsonb_typeof(url) <> 'string'
        OR btrim(url #>> '{}') !~ $url$^https?://[^[:space:]/?#@]+(/[^[:space:]?#]*)?$$url$
      ) THEN 'remove-definition'
      WHEN headers IS NULL THEN 'keep'
      WHEN jsonb_typeof(headers) = 'string' AND btrim(headers #>> '{}') = '' THEN 'remove-empty'
      WHEN jsonb_typeof(headers) = 'string'
        AND NOT EXISTS (
          SELECT 1
          FROM regexp_split_to_table(replace(headers #>> '{}', E'\r\n', E'\n'), E'\n') AS header(line)
          WHERE header.line !~ $header$^[[:space:]]*[A-Za-z0-9!#$%&'*+.^_`|~-]+[[:space:]]*=[[:space:]]*(env|secret)://[A-Za-z_][A-Za-z0-9_]*[[:space:]]*$$header$
            OR lower(btrim(split_part(header.line, '=', 1))) IN (
              'accept', 'content-type', 'connection', 'host', 'content-length',
              'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te',
              'trailer', 'transfer-encoding', 'upgrade', 'mcp-protocol-version',
              'mcp-session-id', 'last-event-id', 'mcp-method', 'mcp-name'
            )
        )
        AND NOT EXISTS (
          SELECT 1
          FROM regexp_split_to_table(replace(headers #>> '{}', E'\r\n', E'\n'), E'\n') AS header(line)
          GROUP BY lower(btrim(split_part(header.line, '=', 1)))
          HAVING count(*) > 1
        ) THEN 'convert'
      WHEN jsonb_typeof(headers) = 'array'
        AND NOT EXISTS (
          SELECT 1
          FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(headers) = 'array' THEN headers ELSE '[]'::jsonb END
          ) AS header(value)
          WHERE jsonb_typeof(header.value) <> 'object'
            OR (CASE WHEN jsonb_typeof(header.value) = 'object'
                  THEN header.value ELSE '{}'::jsonb END - 'name' - 'valueFrom') <> '{}'::jsonb
            OR COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
                  THEN header.value->>'name' END, '') !~ $header$^[A-Za-z0-9!#$%&'*+.^_`|~-]+$$header$
            OR COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
                  THEN header.value->>'valueFrom' END, '') !~ $reference$^(env|secret)://[A-Za-z_][A-Za-z0-9_]*$$reference$
            OR lower(COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
                  THEN header.value->>'name' END, '')) IN (
              'accept', 'content-type', 'connection', 'host', 'content-length',
              'keep-alive', 'proxy-authenticate', 'proxy-authorization', 'te',
              'trailer', 'transfer-encoding', 'upgrade', 'mcp-protocol-version',
              'mcp-session-id', 'last-event-id', 'mcp-method', 'mcp-name'
            )
        )
        AND NOT EXISTS (
          SELECT 1
          FROM jsonb_array_elements(
            CASE WHEN jsonb_typeof(headers) = 'array' THEN headers ELSE '[]'::jsonb END
          ) AS header(value)
          GROUP BY lower(COALESCE(CASE WHEN jsonb_typeof(header.value) = 'object'
            THEN header.value->>'name' END, ''))
          HAVING count(*) > 1
        ) THEN 'keep'
      ELSE 'remove'
    END AS disposition
  FROM definitions
), transformed AS (
  SELECT job_id, ordinality, disposition,
    CASE disposition
      WHEN 'remove-definition' THEN NULL
      WHEN 'convert' THEN jsonb_set(definition, '{spec,headers}', (
        SELECT jsonb_agg(jsonb_build_object(
          'name', btrim(split_part(header.line, '=', 1)),
          'valueFrom', btrim(substring(header.line FROM position('=' IN header.line) + 1))
        ) ORDER BY header.ordinality)
        FROM regexp_split_to_table(replace(headers #>> '{}', E'\r\n', E'\n'), E'\n')
          WITH ORDINALITY AS header(line, ordinality)
      ), TRUE)
      WHEN 'remove' THEN definition #- '{spec,headers}'
      WHEN 'remove-empty' THEN definition #- '{spec,headers}'
      ELSE definition
    END AS definition
  FROM classified
), rebuilt AS (
  SELECT job_id, COALESCE(
      jsonb_agg(definition ORDER BY ordinality) FILTER (WHERE definition IS NOT NULL),
      '[]'::jsonb
    ) AS definitions,
    bool_or(disposition <> 'keep') AS changed,
    bool_or(disposition = 'remove') AS removed_unsafe,
    bool_or(disposition = 'remove-definition') AS removed_unsafe_url
  FROM transformed
  GROUP BY job_id
), updated AS (
  UPDATE worker_jobs job
  SET payload = jsonb_set(job.payload, '{mcpServers}', rebuilt.definitions, TRUE)
  FROM rebuilt
  WHERE job.id = rebuilt.job_id AND rebuilt.changed
  RETURNING job.id, rebuilt.removed_unsafe, rebuilt.removed_unsafe_url
)
INSERT INTO system_logs(level, category, action, message, resource_kind, resource_id, status, detail)
SELECT 'warn', 'job', CASE WHEN updated.removed_unsafe_url THEN 'mcp-url-removed' ELSE 'mcp-headers-removed' END,
  CASE WHEN updated.removed_unsafe_url
    THEN 'An unsafe MCP definition was removed from a Worker job payload during migration'
    ELSE 'Literal or malformed MCP headers were removed from a Worker job payload during migration'
  END,
  'job', updated.id::text, 'success',
  '{"migration":"0010_remove_legacy_mcp_headers","shape":"worker-job-payload"}'::jsonb
FROM updated
WHERE updated.removed_unsafe OR updated.removed_unsafe_url;

-- Older Workers could persist guest-controlled diagnostics. They cannot be
-- made safe after the fact because a protected value may have been transformed
-- before it was returned. Retain structural status/timing columns, but remove
-- every free-form sink for jobs that touched a sandbox or proxy credential.
CREATE TEMP TABLE migration_0010_guest_jobs (
  id UUID PRIMARY KEY,
  resource_id TEXT
) ON COMMIT DROP;

INSERT INTO migration_0010_guest_jobs(id, resource_id)
SELECT id, resource_id
FROM worker_jobs
WHERE action LIKE '%sandbox%'
  OR action LIKE 'workspace-%'
  OR action = 'check-network-proxy';

UPDATE worker_jobs job
SET result_message = CASE
      WHEN job.status IN ('succeeded', 'failed')
        THEN 'Historical Worker diagnostic removed during security migration'
      ELSE ''
    END,
    result_output = '',
    result_truncated = FALSE,
    external_id = CASE
      WHEN job.status = 'succeeded'
        AND job.action IN ('create-sandbox', 'start-sandbox', 'stop-sandbox', 'restart-sandbox')
        AND job.resource_id ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
        AND length(job.resource_id) BETWEEN 2 AND 64
        THEN 'agentbox-' || job.resource_id
      ELSE ''
    END,
    progress = '{}'::jsonb,
    result_error_details = '{}'::jsonb
FROM migration_0010_guest_jobs affected
WHERE job.id = affected.id;

-- Error code/stage are protocol tokens, but an older leased Worker could still
-- derive either token from guest stderr. Rebuild both from the trusted action
-- and a closed vocabulary, then derive retryability from that canonical pair.
WITH canonical_codes AS (
  SELECT job.id, job.action, job.status, job.result_error_stage,
    CASE
      WHEN job.status <> 'failed' THEN ''
      WHEN job.result_error_code IN ('worker_failed', 'worker_timeout', 'worker_action_unsupported')
        THEN job.result_error_code
      WHEN job.action = 'create-sandbox' AND job.result_error_code IN (
        'sandbox_create_failed', 'sandbox_create_cleanup_failed', 'worker_interrupted',
        'worker_interrupted_cleanup_failed', 'job_cancelled', 'cancellation_cleanup_failed'
      ) THEN job.result_error_code
      WHEN job.action = 'start-sandbox' AND job.result_error_code = 'start_sandbox_failed'
        THEN job.result_error_code
      WHEN job.action = 'stop-sandbox' AND job.result_error_code = 'stop_sandbox_failed'
        THEN job.result_error_code
      WHEN job.action = 'restart-sandbox' AND job.result_error_code = 'restart_sandbox_failed'
        THEN job.result_error_code
      WHEN job.action = 'delete-sandbox' AND job.result_error_code = 'delete_sandbox_failed'
        THEN job.result_error_code
      WHEN job.action = 'check-network-proxy' AND job.result_error_code = 'network_proxy_check_failed'
        THEN job.result_error_code
      WHEN job.action = 'check-sandbox-agent-tools'
        AND job.result_error_code = 'sandbox_agent_tools_check_failed' THEN job.result_error_code
      WHEN job.action = 'update-sandbox-agent-tools'
        AND job.result_error_code = 'sandbox_agent_tools_update_failed' THEN job.result_error_code
      WHEN job.action = 'configure-sandbox-proxy'
        AND job.result_error_code = 'sandbox_proxy_apply_failed' THEN job.result_error_code
      WHEN job.action = 'create-sandbox' THEN 'sandbox_create_failed'
      WHEN job.action = 'start-sandbox' THEN 'start_sandbox_failed'
      WHEN job.action = 'stop-sandbox' THEN 'stop_sandbox_failed'
      WHEN job.action = 'restart-sandbox' THEN 'restart_sandbox_failed'
      WHEN job.action = 'delete-sandbox' THEN 'delete_sandbox_failed'
      WHEN job.action = 'check-network-proxy' THEN 'network_proxy_check_failed'
      WHEN job.action = 'check-sandbox-agent-tools' THEN 'sandbox_agent_tools_check_failed'
      WHEN job.action = 'update-sandbox-agent-tools' THEN 'sandbox_agent_tools_update_failed'
      WHEN job.action = 'configure-sandbox-proxy' THEN 'sandbox_proxy_apply_failed'
      ELSE 'worker_failed'
    END AS safe_code
  FROM worker_jobs job
  JOIN migration_0010_guest_jobs affected ON affected.id = job.id
), canonical_stages AS (
  SELECT codes.id, codes.action, codes.safe_code,
    CASE
      WHEN codes.status <> 'failed' THEN ''
      WHEN codes.safe_code IN ('job_cancelled', 'cancellation_cleanup_failed') THEN 'cancel'
      WHEN codes.safe_code IN ('sandbox_create_cleanup_failed', 'worker_interrupted_cleanup_failed')
        THEN 'cleanup'
      WHEN codes.safe_code = 'worker_interrupted' THEN 'create'
      WHEN codes.safe_code = 'worker_timeout' THEN 'execution'
      WHEN codes.safe_code = 'worker_action_unsupported' THEN 'dispatch'
      WHEN codes.action = 'create-sandbox' AND codes.result_error_stage IN (
        'agent-tools', 'agent-wrappers', 'cleanup', 'create', 'create-result', 'credentials',
        'desktop-config', 'desktop-start', 'extensions', 'image-prepare', 'manifest-write',
        'mcp', 'network-policy', 'proxy-config', 'runtime-create', 'runtime-image',
        'runtime-probe', 'setup-command', 'skills', 'variables', 'workspace-init'
      ) THEN codes.result_error_stage
      WHEN codes.action = 'start-sandbox' AND codes.result_error_stage IN (
        'agent-tools', 'agent-wrappers', 'credentials', 'manifest-write', 'mcp',
        'network-policy', 'proxy-config', 'runtime-probe', 'skills', 'start', 'variables'
      ) THEN codes.result_error_stage
      WHEN codes.action = 'restart-sandbox' AND codes.result_error_stage IN (
        'agent-tools', 'agent-wrappers', 'credentials', 'manifest-write', 'mcp',
        'network-policy', 'proxy-config', 'restart', 'runtime-probe', 'skills', 'variables'
      ) THEN codes.result_error_stage
      WHEN codes.action = 'stop-sandbox' AND codes.result_error_stage = 'stop'
        THEN codes.result_error_stage
      WHEN codes.action = 'delete-sandbox' AND codes.result_error_stage = 'delete'
        THEN codes.result_error_stage
      WHEN codes.action = 'check-network-proxy' AND codes.result_error_stage = 'proxy-check'
        THEN codes.result_error_stage
      WHEN codes.action IN ('check-sandbox-agent-tools', 'update-sandbox-agent-tools')
        AND codes.result_error_stage = 'agent-tools' THEN codes.result_error_stage
      WHEN codes.action = 'configure-sandbox-proxy' AND codes.result_error_stage = 'proxy-config'
        THEN codes.result_error_stage
      WHEN codes.action = 'create-sandbox' THEN 'create'
      WHEN codes.action = 'start-sandbox' THEN 'start'
      WHEN codes.action = 'stop-sandbox' THEN 'stop'
      WHEN codes.action = 'restart-sandbox' THEN 'restart'
      WHEN codes.action = 'delete-sandbox' THEN 'delete'
      WHEN codes.action = 'check-network-proxy' THEN 'proxy-check'
      WHEN codes.action IN ('check-sandbox-agent-tools', 'update-sandbox-agent-tools') THEN 'agent-tools'
      WHEN codes.action = 'configure-sandbox-proxy' THEN 'proxy-config'
      WHEN codes.action LIKE 'workspace-%' THEN 'workspace-init'
      ELSE 'execution'
    END AS safe_stage
  FROM canonical_codes codes
), canonical_errors AS (
  SELECT stages.id, stages.safe_code, stages.safe_stage,
    CASE
      WHEN stages.safe_code IN ('worker_timeout', 'network_proxy_check_failed', 'sandbox_proxy_apply_failed')
        THEN TRUE
      WHEN stages.safe_code = 'sandbox_create_failed' AND stages.safe_stage IN (
        'runtime-probe', 'runtime-image', 'image-prepare', 'runtime-create'
      ) THEN TRUE
      ELSE FALSE
    END AS safe_retryable
  FROM canonical_stages stages
)
UPDATE worker_jobs job
SET result_error_code = safe.safe_code,
    result_error_stage = safe.safe_stage,
    result_error_retryable = safe.safe_retryable,
    result_timed_out = safe.safe_code = 'worker_timeout'
FROM canonical_errors safe
WHERE job.id = safe.id;

-- The source job may already have been removed by retention while Sandbox
-- observations live indefinitely. Discover these sinks from the Sandbox row
-- itself, not through worker_jobs. The selected fields are all Worker-derived
-- observations; desired configuration and structured lifecycle state remain.
CREATE TEMP TABLE migration_0010_historical_sandbox_sink_ids (
  id TEXT PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO migration_0010_historical_sandbox_sink_ids(id)
SELECT sandbox.id
FROM control_resources sandbox
WHERE sandbox.kind = 'sandbox'
  AND (
    COALESCE(sandbox.spec->>'message', '') <> ''
    OR COALESCE(sandbox.spec->>'externalId', '') <> ''
    OR sandbox.spec ?| ARRAY[
      'provisioning', 'extensionStates', 'agentToolVersions',
      'agentToolOperation', 'proxyOperation'
    ]
  );

-- Instance names have always been deterministic; recover that value from the
-- validated resource id instead of trusting a historical Worker response.
UPDATE control_resources sandbox
SET spec = (
      sandbox.spec
        - 'message' - 'provisioning' - 'extensionStates' - 'agentToolVersions'
        - 'agentToolOperation' - 'proxyOperation' - 'externalId'
    ) || jsonb_build_object(
      'message', CASE
        WHEN COALESCE(sandbox.spec->>'externalId', '') <> ''
          AND NOT (
            sandbox.id ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
            AND length(sandbox.id) BETWEEN 2 AND 64
          )
          THEN 'Historical sandbox identity was invalid and requires operator attention'
        ELSE 'Historical Worker diagnostic removed during security migration'
      END,
      'externalId', CASE
        WHEN COALESCE(sandbox.spec->>'externalId', '') <> ''
          AND sandbox.id ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
          AND length(sandbox.id) BETWEEN 2 AND 64
          THEN 'agentbox-' || sandbox.id
        ELSE ''
      END,
      'status', CASE
        WHEN COALESCE(sandbox.spec->>'externalId', '') <> ''
          AND NOT (
            sandbox.id ~ '^[a-z0-9]+(-[a-z0-9]+)*$'
            AND length(sandbox.id) BETWEEN 2 AND 64
          )
          THEN 'error'
        ELSE COALESCE(sandbox.spec->>'status', 'error')
      END
    ),
    updated_at = NOW()
WHERE sandbox.kind = 'sandbox'
  AND EXISTS (
    SELECT 1 FROM migration_0010_historical_sandbox_sink_ids affected
    WHERE affected.id = sandbox.id
  );

-- automation_runs outlive terminal Worker jobs and their foreign key is SET
-- NULL on retention. Find free-form Worker sinks directly, and independently
-- canonicalize structured error tokens that an older Worker could control.
-- Legacy actions and statuses are no longer exposed by the current API. Remove
-- those unreachable rows before updating retained history: PostgreSQL still
-- enforces NOT VALID constraints on rows touched by an UPDATE.
DELETE FROM automation_runs
WHERE action_type <> 'create-sandbox'
  OR status NOT IN ('evaluating', 'queued', 'provisioning', 'succeeded', 'failed');

CREATE TEMP TABLE migration_0010_historical_automation_sink_ids (
  id UUID PRIMARY KEY
) ON COMMIT DROP;

INSERT INTO migration_0010_historical_automation_sink_ids(id)
SELECT run.id
FROM automation_runs run
WHERE run.error_message <> ''
  OR run.error_code <> ''
  OR run.error_stage <> ''
  OR run.error_details <> '{}'::jsonb
  OR run.provisioning <> '{}'::jsonb;

WITH canonical_codes AS (
  SELECT run.id, run.status, run.error_stage,
    CASE
      WHEN run.status <> 'failed' THEN ''
      WHEN run.error_code = '' AND run.error_stage = '' THEN ''
      WHEN run.error_code IN (
        'template_invalid', 'input_invalid', 'worker_failed', 'worker_timeout',
        'worker_action_unsupported', 'sandbox_create_failed', 'sandbox_create_cleanup_failed',
        'worker_interrupted', 'worker_interrupted_cleanup_failed', 'job_cancelled',
        'cancellation_cleanup_failed', 'worker_unavailable', 'worker_lease_expired',
        'worker_retry_exhausted', 'resource_generation_changed',
        'sandbox_proxy_provenance_invalid', 'sandbox_extension_unverified'
      ) THEN run.error_code
      ELSE 'worker_failed'
    END AS safe_code
  FROM automation_runs run
  JOIN migration_0010_historical_automation_sink_ids affected ON affected.id = run.id
), canonical_stages AS (
  SELECT codes.id, codes.safe_code,
    CASE
      WHEN codes.status <> 'failed' OR codes.safe_code = '' THEN ''
      WHEN codes.safe_code IN ('job_cancelled', 'cancellation_cleanup_failed') THEN 'cancel'
      WHEN codes.safe_code IN ('sandbox_create_cleanup_failed', 'worker_interrupted_cleanup_failed')
        THEN 'cleanup'
      WHEN codes.safe_code = 'worker_interrupted' THEN 'create'
      WHEN codes.safe_code = 'worker_timeout' THEN 'execution'
      WHEN codes.safe_code = 'worker_action_unsupported' THEN 'dispatch'
      WHEN codes.error_stage IN (
        '', 'agent-tools', 'agent-wrappers', 'cancel', 'cleanup', 'create', 'create-result',
        'credentials', 'desktop-config', 'desktop-start', 'dispatch', 'execution', 'extensions',
        'image-prepare', 'manifest-write', 'mcp', 'network-policy', 'proxy-config',
        'runtime-create', 'runtime-image', 'runtime-probe', 'setup-command', 'skills',
        'variables', 'workspace-init'
      ) THEN codes.error_stage
      ELSE 'create'
    END AS safe_stage
  FROM canonical_codes codes
), canonical_errors AS (
  SELECT stages.id, stages.safe_code, stages.safe_stage,
    CASE
      WHEN stages.safe_code IN ('worker_timeout', 'worker_unavailable') THEN TRUE
      WHEN stages.safe_code = 'sandbox_create_failed' AND stages.safe_stage IN (
        'runtime-probe', 'runtime-image', 'image-prepare', 'runtime-create'
      ) THEN TRUE
      ELSE FALSE
    END AS safe_retryable
  FROM canonical_stages stages
)
UPDATE automation_runs run
SET error_message = CASE WHEN run.error_message = '' THEN ''
      ELSE 'Historical Worker diagnostic removed during security migration' END,
    error_code = safe.safe_code,
    error_stage = safe.safe_stage,
    error_retryable = safe.safe_retryable,
    error_details = '{}'::jsonb,
    provisioning = '{}'::jsonb
FROM canonical_errors safe
WHERE safe.id = run.id;

-- Header semantics may already have been copied into a guest. Keep lifecycle
-- generation stable for in-flight create/start/restart completion, and advance
-- only the capability revision so completion cannot falsely mark old content as
-- applied. A later managed restart reconciles the exact configuration.
UPDATE control_resources sandbox
SET spec = jsonb_set(
      jsonb_set(sandbox.spec, '{capabilitiesPendingRestart}', 'true'::jsonb, TRUE),
      '{capabilityRevision}',
      to_jsonb((CASE
        WHEN COALESCE(sandbox.spec->>'capabilityRevision', '') ~ '^[0-9]+$'
          THEN (sandbox.spec->>'capabilityRevision')::bigint
        ELSE 0
      END) + 1), TRUE
    ),
    updated_at = NOW()
WHERE sandbox.kind = 'sandbox' AND (
  EXISTS (
    SELECT 1
    FROM migration_0010_affected_sandbox_ids affected
    WHERE affected.id = sandbox.id
  )
  OR EXISTS (
    SELECT 1
    FROM migration_0010_affected_mcp_ids affected
    WHERE sandbox.spec->'mcpServerIds' ? affected.id
      OR (
        NOT (sandbox.spec ? 'mcpServerIds') AND EXISTS (
          SELECT 1
          FROM control_resources runtime
          WHERE runtime.id = sandbox.spec->>'runtimeId' AND runtime.kind = 'runtime'
            AND runtime.spec->'mcpServerIds' ? affected.id
        )
      )
  )
);

-- The original Variable shape omitted mode. Infer it only when the reference
-- itself is an unambiguous managed scheme; explicit modes remain untouched.
UPDATE control_resources
SET spec = jsonb_set(spec, '{mode}', to_jsonb(
  CASE WHEN spec->>'reference' LIKE 'env://%' THEN 'value-ref' ELSE 'secret-ref' END
), TRUE)
WHERE kind = 'variable'
  AND COALESCE(spec->>'mode', '') = ''
  AND spec->>'reference' ~ $reference$^(env|secret)://[A-Za-z_][A-Za-z0-9_]*$$reference$;
