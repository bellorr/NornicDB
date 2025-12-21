# User Storage in System Database - Implementation Plan

## Overview

This document outlines the plan to migrate user storage from in-memory maps to persistent storage in the system database, following industry-standard practices.

## Current State

- **Storage**: In-memory `map[string]*User` in `Authenticator` struct
- **Persistence**: None - users are lost on restart
- **Password Security**: ✅ Already correct - bcrypt with automatic salt
- **System Database**: Exists and is used for database metadata

## Target State

- **Storage**: Users stored as nodes in the system database
- **Persistence**: Users survive restarts
- **Password Security**: ✅ Unchanged - bcrypt (salt embedded in hash)
- **Backwards Compatibility**: Support both in-memory (dev) and persistent (prod) modes

## Design

### User Node Structure

Users will be stored as nodes in the system database with:

- **Labels**: `["_User", "_System"]`
- **Node ID**: `user:{username}` (e.g., `user:admin`)
- **Properties**:
  ```json
  {
    "id": "usr-abc123",
    "username": "admin",
    "email": "admin@example.com",
    "password_hash": "$2a$10$...",  // bcrypt hash (includes salt)
    "roles": ["admin"],
    "created_at": "2024-01-01T00:00:00Z",
    "updated_at": "2024-01-01T00:00:00Z",
    "last_login": "2024-01-01T00:00:00Z",
    "failed_logins": 0,
    "locked_until": null,
    "disabled": false,
    "metadata": {}
  }
  ```

### Implementation Approach

1. **Add Storage Engine to Authenticator**
   - Make storage engine optional (for backwards compatibility)
   - If provided, use persistent storage
   - If nil, use in-memory (current behavior)

2. **Load Users on Startup**
   - Query system database for all `_User` nodes
   - Deserialize into `User` structs
   - Populate in-memory cache for fast lookups

3. **Save Users on Create/Update**
   - Create/update node in system database
   - Update in-memory cache
   - Use transactions for consistency

4. **Query Pattern**
   ```cypher
   MATCH (u:_User {username: $username})
   RETURN u
   ```

### Code Changes

#### 1. Update Authenticator Struct

```go
type Authenticator struct {
    mu     sync.RWMutex
    users  map[string]*User // In-memory cache (fast lookups)
    config AuthConfig
    
    // Optional: Storage engine for persistence
    storage storage.Engine // nil = in-memory only
    
    // Audit callback
    auditLog func(event AuditEvent)
}
```

#### 2. Update NewAuthenticator

```go
func NewAuthenticator(config AuthConfig, storage storage.Engine) (*Authenticator, error) {
    auth := &Authenticator{
        users:  make(map[string]*User),
        config: config,
        storage: storage,
    }
    
    // Load users from storage if available
    if storage != nil {
        if err := auth.loadUsers(); err != nil {
            return nil, fmt.Errorf("failed to load users: %w", err)
        }
    }
    
    return auth, nil
}
```

#### 3. Add Load/Save Methods

```go
// loadUsers loads all users from system database
func (a *Authenticator) loadUsers() error {
    // Query system database for _User nodes
    // Deserialize and populate a.users map
}

// saveUser saves a user to system database
func (a *Authenticator) saveUser(user *User) error {
    // Create/update node in system database
    // Update in-memory cache
}
```

#### 4. Update CreateUser

```go
func (a *Authenticator) CreateUser(username, password string, roles []Role) (*User, error) {
    // ... existing validation and hashing ...
    
    user := &User{...}
    
    // Save to storage if available
    if a.storage != nil {
        if err := a.saveUser(user); err != nil {
            return nil, err
        }
    }
    
    // Update in-memory cache
    a.users[username] = user
    
    return a.copyUserSafe(user), nil
}
```

## Migration Strategy

### Phase 1: Add Storage Support (Backwards Compatible)
- Add optional storage engine parameter
- Implement load/save methods
- Keep in-memory cache for performance
- All existing code continues to work (storage=nil)

### Phase 2: Update Server Initialization
- Pass system database storage engine to Authenticator
- Users automatically persist on create/update

### Phase 3: Migration Script (Optional)
- If needed, migrate existing in-memory users to database
- Run once on first startup with storage enabled

## Security Considerations

✅ **Already Implemented Correctly:**
- Passwords hashed with bcrypt (includes salt automatically)
- Password hash never serialized in JSON responses
- Account lockout and failed login tracking

✅ **Additional Benefits:**
- Users can be backed up with database backup
- GDPR deletion can remove user nodes
- Audit trail in system database

## Performance

- **In-Memory Cache**: Fast lookups (O(1))
- **Storage Writes**: Only on create/update (infrequent)
- **Storage Reads**: Only on startup (once)
- **Impact**: Minimal - authentication still fast

## Testing

1. **Unit Tests**: Test load/save methods
2. **Integration Tests**: Test with real storage engine
3. **Migration Tests**: Test loading existing users
4. **Backwards Compatibility**: Test with storage=nil

## Example Usage

```go
// Production: With persistence
systemStorage := dbManager.GetStorage("system")
auth, err := auth.NewAuthenticator(config, systemStorage)

// Development: In-memory only
auth, err := auth.NewAuthenticator(config, nil)
```

## Benefits

1. ✅ **Persistence**: Users survive restarts
2. ✅ **Backup**: Users included in database backups
3. ✅ **Recovery**: Can restore users from backup
4. ✅ **Compliance**: GDPR export/deletion works automatically
5. ✅ **Multi-Instance**: Can share user data across instances
6. ✅ **Audit**: User changes tracked in database
7. ✅ **Standard Practice**: Matches industry standards (PostgreSQL, MySQL, etc.)

## Industry Standards

This approach matches how major systems store users:

- **PostgreSQL**: `pg_authid` system table
- **MySQL**: `mysql.user` system table
- **Neo4j**: `:User` nodes in system database
- **MongoDB**: `admin.users` collection
- **LDAP**: `ou=users` organizational unit

## Next Steps

1. Review and approve this design
2. Implement Phase 1 (add storage support)
3. Update server initialization
4. Add tests
5. Document migration path

