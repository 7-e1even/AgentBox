ALTER TABLE control_resources DROP CONSTRAINT control_resources_kind_check;
ALTER TABLE control_resources ADD CONSTRAINT control_resources_kind_check CHECK (kind IN (
  'project', 'image', 'runtime', 'skill', 'mcp', 'sandbox', 'variable', 'extension'
));
