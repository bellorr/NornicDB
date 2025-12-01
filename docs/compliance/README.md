# Compliance & Security

**GDPR, HIPAA, SOC2 compliance and security features.**

## 📚 Documentation

- **[GDPR Compliance](gdpr-compliance.md)** - EU data protection
- **[HIPAA Compliance](hipaa-compliance.md)** - Healthcare data security
- **[SOC2 Compliance](soc2-compliance.md)** - Service organization controls
- **[Encryption](encryption.md)** - Data encryption at rest and in transit
- **[Audit Logging](audit-logging.md)** - Comprehensive audit trails
- **[RBAC](rbac.md)** - Role-based access control

## 🔒 Security Features

### Encryption
- **AES-256-GCM** encryption at rest
- **TLS 1.3** for data in transit
- **Field-level encryption** for sensitive data
- **Key rotation** support

[Encryption guide →](encryption.md)

### Access Control
- **JWT authentication** with RBAC
- **Fine-grained permissions** (read/write/admin)
- **API key management**
- **Session management**

[RBAC guide →](rbac.md)

### Audit Logging
- **Comprehensive audit trails** for all operations
- **HIPAA-compliant** logging
- **Tamper-proof** log storage
- **Log retention** policies

[Audit logging guide →](audit-logging.md)

## 📋 Compliance Standards

### GDPR (EU)
- Right of access (Art. 15)
- Right to erasure (Art. 17)
- Data portability (Art. 20)
- Privacy by design

[GDPR compliance →](gdpr-compliance.md)

### HIPAA (US Healthcare)
- PHI encryption
- Access controls
- Audit trails
- Breach notification

[HIPAA compliance →](hipaa-compliance.md)

### SOC2 (Service Organizations)
- Security controls
- Availability
- Processing integrity
- Confidentiality

[SOC2 compliance →](soc2-compliance.md)

## 🚀 Quick Start

### Enable Encryption

```yaml
encryption:
  enabled: true
  algorithm: AES-256-GCM
  key_rotation: 90d
```

### Enable Audit Logging

```yaml
audit:
  enabled: true
  log_all_queries: true
  retention_days: 365
```

### Configure RBAC

```yaml
auth:
  enabled: true
  rbac:
    roles:
      - name: admin
        permissions: [read, write, admin]
      - name: user
        permissions: [read, write]
      - name: readonly
        permissions: [read]
```

## 📖 Learn More

- **[Encryption](encryption.md)** - Data protection
- **[RBAC](rbac.md)** - Access control
- **[Audit Logging](audit-logging.md)** - Compliance trails

---

**Secure your database** → **[Encryption Guide](encryption.md)**
