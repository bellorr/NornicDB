// ============================================================================
// Canonical Graph Schema Bootstrap
// ============================================================================
// This script creates all constraints and indexes for Idea #7 canonical graph.
// Run once per database (schema persists across restarts).
// All statements are idempotent (safe to run multiple times).

// ----------------------------------------------------------------------------
// Entity Constraints
// ----------------------------------------------------------------------------

// Required fields
CREATE CONSTRAINT entity_id_required IF NOT EXISTS 
FOR (n:Entity) REQUIRE n.entity_id IS NOT NULL;

CREATE CONSTRAINT entity_type_required IF NOT EXISTS 
FOR (n:Entity) REQUIRE n.entity_type IS NOT NULL;

// Uniqueness
CREATE CONSTRAINT entity_id_unique IF NOT EXISTS 
FOR (n:Entity) REQUIRE n.entity_id IS UNIQUE;

// ----------------------------------------------------------------------------
// FactKey Constraints (composite uniqueness via NODE KEY)
// ----------------------------------------------------------------------------

// Required fields
CREATE CONSTRAINT fact_key_subject_required IF NOT EXISTS 
FOR (n:FactKey) REQUIRE n.subject_entity_id IS NOT NULL;

CREATE CONSTRAINT fact_key_predicate_required IF NOT EXISTS 
FOR (n:FactKey) REQUIRE n.predicate IS NOT NULL;

// Composite uniqueness (replaces computed fact_key property)
CREATE CONSTRAINT fact_key_node_key IF NOT EXISTS 
FOR (n:FactKey) REQUIRE (n.subject_entity_id, n.predicate) IS NODE KEY;

// ----------------------------------------------------------------------------
// FactVersion Constraints
// ----------------------------------------------------------------------------

// Required fields
CREATE CONSTRAINT fact_version_fact_key_required IF NOT EXISTS 
FOR (n:FactVersion) REQUIRE n.fact_key IS NOT NULL;

CREATE CONSTRAINT fact_version_valid_from_required IF NOT EXISTS 
FOR (n:FactVersion) REQUIRE n.valid_from IS NOT NULL;

CREATE CONSTRAINT fact_version_value_json_required IF NOT EXISTS 
FOR (n:FactVersion) REQUIRE n.value_json IS NOT NULL;

CREATE CONSTRAINT fact_version_asserted_at_required IF NOT EXISTS 
FOR (n:FactVersion) REQUIRE n.asserted_at IS NOT NULL;

CREATE CONSTRAINT fact_version_asserted_by_required IF NOT EXISTS 
FOR (n:FactVersion) REQUIRE n.asserted_by IS NOT NULL;

// Type constraints
CREATE CONSTRAINT fact_version_valid_from_type IF NOT EXISTS 
FOR (n:FactVersion) REQUIRE n.valid_from IS :: DATETIME;

CREATE CONSTRAINT fact_version_valid_to_type IF NOT EXISTS 
FOR (n:FactVersion) REQUIRE n.valid_to IS :: DATETIME;

CREATE CONSTRAINT fact_version_asserted_at_type IF NOT EXISTS 
FOR (n:FactVersion) REQUIRE n.asserted_at IS :: DATETIME;

// Temporal no-overlap constraint (NornicDB extension)
CREATE CONSTRAINT fact_version_temporal_no_overlap IF NOT EXISTS
FOR (n:FactVersion) REQUIRE (n.fact_key, n.valid_from, n.valid_to) IS TEMPORAL NO OVERLAP;

// ----------------------------------------------------------------------------
// MutationEvent Constraints
// ----------------------------------------------------------------------------

// Required fields
CREATE CONSTRAINT mutation_event_id_required IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.event_id IS NOT NULL;

CREATE CONSTRAINT mutation_event_tx_id_required IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.tx_id IS NOT NULL;

CREATE CONSTRAINT mutation_event_actor_required IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.actor IS NOT NULL;

CREATE CONSTRAINT mutation_event_timestamp_required IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.timestamp IS NOT NULL;

CREATE CONSTRAINT mutation_event_op_type_required IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.op_type IS NOT NULL;

// Uniqueness
CREATE CONSTRAINT mutation_event_id_unique IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.event_id IS UNIQUE;

// Type constraints
CREATE CONSTRAINT mutation_event_timestamp_type IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.timestamp IS :: DATETIME;

CREATE CONSTRAINT mutation_event_op_type_string IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.op_type IS :: STRING;

CREATE CONSTRAINT mutation_event_actor_string IF NOT EXISTS 
FOR (n:MutationEvent) REQUIRE n.actor IS :: STRING;

// ----------------------------------------------------------------------------
// Evidence Constraints (optional, adjust as needed)
// ----------------------------------------------------------------------------

CREATE CONSTRAINT evidence_id_required IF NOT EXISTS 
FOR (n:Evidence) REQUIRE n.evidence_id IS NOT NULL;

CREATE CONSTRAINT evidence_id_unique IF NOT EXISTS 
FOR (n:Evidence) REQUIRE n.evidence_id IS UNIQUE;

// ----------------------------------------------------------------------------
// Vector Indexes
// ----------------------------------------------------------------------------

// Vector index for canonical fact search
// Adjust dimensions to match your embedding model (e.g., 1024 for OpenAI, 384 for sentence-transformers)
CREATE VECTOR INDEX canonical_fact_idx IF NOT EXISTS
FOR (n:FactVersion) ON (n.embedding)
OPTIONS {indexConfig: {`vector.dimensions`: 1024, `vector.similarity_function`: 'cosine'}};

// Vector index for evidence/document search
CREATE VECTOR INDEX evidence_content_idx IF NOT EXISTS
FOR (n:Evidence) ON (n.embedding)
OPTIONS {indexConfig: {`vector.dimensions`: 1024, `vector.similarity_function`: 'cosine'}};

// ----------------------------------------------------------------------------
// Property Indexes (for efficient lookups)
// ----------------------------------------------------------------------------

// Index for entity type lookups
CREATE INDEX entity_type_idx IF NOT EXISTS 
FOR (n:Entity) ON (n.entity_type);

// Index for fact version temporal queries
CREATE INDEX fact_version_valid_from_idx IF NOT EXISTS 
FOR (n:FactVersion) ON (n.valid_from);

CREATE INDEX fact_version_fact_key_idx IF NOT EXISTS 
FOR (n:FactVersion) ON (n.fact_key);

// Index for mutation event queries
CREATE INDEX mutation_event_tx_id_idx IF NOT EXISTS 
FOR (n:MutationEvent) ON (n.tx_id);

CREATE INDEX mutation_event_timestamp_idx IF NOT EXISTS 
FOR (n:MutationEvent) ON (n.timestamp);

CREATE INDEX mutation_event_actor_idx IF NOT EXISTS 
FOR (n:MutationEvent) ON (n.actor);

// ----------------------------------------------------------------------------
// Verify Schema
// ----------------------------------------------------------------------------

// List all constraints
CALL db.constraints() YIELD name, type, labelsOrTypes, properties
RETURN name, type, labelsOrTypes, properties
ORDER BY labelsOrTypes[0], name;

// List all indexes
SHOW INDEXES;
