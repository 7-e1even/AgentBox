UPDATE control_resources AS resource
SET spec = jsonb_set(resource.spec, '{network}', to_jsonb('egress'::text), TRUE)
WHERE resource.kind IN ('runtime', 'sandbox')
  AND resource.spec->>'network' = 'restricted'
  AND COALESCE(
    NULLIF(resource.spec->>'driver', ''),
    (
      SELECT runtime.spec->>'driver'
      FROM control_resources AS runtime
      WHERE resource.kind = 'sandbox'
        AND runtime.kind = 'runtime'
        AND runtime.id = resource.spec->>'runtimeId'
    )
  ) IN ('docker', 'microsandbox');

UPDATE worker_jobs
SET payload = jsonb_set(payload, '{network}', to_jsonb('egress'::text), TRUE)
WHERE status = 'pending'
  AND action IN ('create-sandbox', 'start-sandbox', 'restart-sandbox')
  AND payload->>'network' = 'restricted'
  AND payload->>'driver' IN ('docker', 'microsandbox');
