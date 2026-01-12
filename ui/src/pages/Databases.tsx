import { useEffect, useState } from 'react';
import { Database, Info, Plus, Trash2 } from 'lucide-react';
import { api, DatabaseInfo } from '../utils/api';
import { Alert } from '../components/common/Alert';
import { Button } from '../components/common/Button';
import { FormInput } from '../components/common/FormInput';
import { Modal } from '../components/common/Modal';
import { PageHeader } from '../components/common/PageHeader';
import { PageLayout } from '../components/common/PageLayout';

export function Databases() {
  const [databases, setDatabases] = useState<DatabaseInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  // Create database state
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newName, setNewName] = useState('');
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState('');

  // Delete database state
  const [deleteTarget, setDeleteTarget] = useState<DatabaseInfo | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');

  // Database details state
  const [selectedDatabase, setSelectedDatabase] = useState<DatabaseInfo | null>(null);
  const [showDetailsModal, setShowDetailsModal] = useState(false);

  useEffect(() => {
    loadDatabases();
  }, []);

  const loadDatabases = async () => {
    try {
      setError('');
      const data = await api.listDatabases();
      setDatabases(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load databases');
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  };

  const handleRefresh = () => {
    setRefreshing(true);
    loadDatabases();
  };

  const handleCreateDatabase = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreateError('');
    setCreating(true);

    try {
      await api.createDatabase(newName);
      setShowCreateModal(false);
      setNewName('');
      await loadDatabases();
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : 'Failed to create database');
    } finally {
      setCreating(false);
    }
  };

  const handleDeleteDatabase = async () => {
    if (!deleteTarget) return;
    setDeleteError('');
    setDeleting(true);

    try {
      await api.dropDatabase(deleteTarget.name);
      setDeleteTarget(null);
      await loadDatabases();
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : 'Failed to delete database');
    } finally {
      setDeleting(false);
    }
  };

  const handleViewDetails = async (db: DatabaseInfo) => {
    try {
      const info = await api.getDatabaseInfo(db.name);
      setSelectedDatabase(info);
      setShowDetailsModal(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load database details');
    }
  };

  if (loading) {
    return (
      <PageLayout>
        <PageHeader title="Databases" backTo="/" backLabel="Back to Browser" />
        <div className="flex items-center justify-center min-h-[400px]">
          <div className="text-norse-silver">Loading databases...</div>
        </div>
      </PageLayout>
    );
  }

  return (
    <PageLayout>
      <PageHeader
        title="Databases"
        backTo="/"
        backLabel="Back to Browser"
        actions={
          <div className="flex items-center gap-2">
            <Button variant="secondary" onClick={handleRefresh} disabled={refreshing}>
              {refreshing ? 'Refreshing...' : 'Refresh'}
            </Button>
            <Button variant="primary" onClick={() => setShowCreateModal(true)} icon={Plus}>
              Create Database
            </Button>
          </div>
        }
      />

      <main className="max-w-6xl mx-auto p-6">
        {error && (
          <Alert
            type="error"
            message={error}
            className="mb-6"
            dismissible
            onDismiss={() => setError('')}
          />
        )}

        {databases.length === 0 ? (
          <div className="bg-norse-shadow border border-norse-rune rounded-lg p-12 text-center">
            <Database className="w-16 h-16 text-norse-silver mx-auto mb-4" />
            <h3 className="text-lg font-semibold text-white mb-2">No Databases</h3>
            <p className="text-norse-silver mb-6">Create your first database to start organizing data</p>
            <Button variant="primary" onClick={() => setShowCreateModal(true)} icon={Plus}>
              Create Database
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {databases.map((db) => (
              <div
                key={db.name}
                className="bg-norse-shadow border border-norse-rune rounded-lg p-4 hover:border-nornic-primary transition-colors"
              >
                <div className="flex items-start justify-between mb-3">
                  <div className="flex-1">
                    <h3 className="text-lg font-semibold text-white mb-1">{db.name}</h3>
                    <div className="flex items-center gap-2 text-sm text-norse-silver">
                      <span
                        className={`px-2 py-1 rounded text-xs ${
                          db.status === 'online' ? 'bg-green-900/30 text-green-400' : 'bg-red-900/30 text-red-400'
                        }`}
                      >
                        {db.status}
                      </span>
                      {db.default && (
                        <span className="px-2 py-1 rounded text-xs bg-valhalla-gold/20 text-valhalla-gold">
                          default
                        </span>
                      )}
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    <div title="View details">
                      <Button variant="ghost" size="sm" onClick={() => handleViewDetails(db)} icon={Info}>
                        <span className="sr-only">View details</span>
                      </Button>
                    </div>
                    <div title={db.default ? 'Default database cannot be deleted' : 'Delete database'}>
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setDeleteTarget(db)}
                        icon={Trash2}
                        disabled={db.default}
                        className="text-red-400 hover:text-red-300 hover:bg-red-900/20 disabled:opacity-40 disabled:hover:bg-transparent"
                      >
                        <span className="sr-only">Delete database</span>
                      </Button>
                    </div>
                  </div>
                </div>

                <div className="space-y-2 text-sm">
                  <div className="flex justify-between">
                    <span className="text-norse-silver">Nodes:</span>
                    <span className="text-white font-medium">{db.nodeCount.toLocaleString()}</span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-norse-silver">Edges:</span>
                    <span className="text-white font-medium">{db.edgeCount.toLocaleString()}</span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </main>

      <Modal isOpen={showCreateModal} onClose={() => setShowCreateModal(false)} title="Create Database" size="md">
        <form onSubmit={handleCreateDatabase} className="space-y-4">
          {createError && <Alert type="error" message={createError} dismissible onDismiss={() => setCreateError('')} />}
          <FormInput
            label="Database Name"
            value={newName}
            onChange={(value) => setNewName(value)}
            placeholder="e.g. tenant_a"
            required
            disabled={creating}
          />
          <div className="text-sm text-norse-silver">
            Creates a new database namespace. Qdrant collections are also mapped to databases.
          </div>
          <div className="flex justify-end gap-2 pt-4">
            <Button type="button" variant="secondary" onClick={() => setShowCreateModal(false)} disabled={creating}>
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={creating || !newName.trim()}>
              {creating ? 'Creating...' : 'Create'}
            </Button>
          </div>
        </form>
      </Modal>

      <Modal
        isOpen={Boolean(deleteTarget)}
        onClose={() => setDeleteTarget(null)}
        title="Delete Database"
        size="md"
      >
        <div className="space-y-4">
          {deleteError && <Alert type="error" message={deleteError} dismissible onDismiss={() => setDeleteError('')} />}
          <p className="text-norse-silver">
            Delete database <span className="text-white font-semibold">{deleteTarget?.name}</span>? This removes all
            data in that namespace.
          </p>
          <div className="flex justify-end gap-2 pt-4">
            <Button type="button" variant="secondary" onClick={() => setDeleteTarget(null)} disabled={deleting}>
              Cancel
            </Button>
            <Button type="button" variant="danger" onClick={handleDeleteDatabase} disabled={deleting}>
              {deleting ? 'Deleting...' : 'Delete'}
            </Button>
          </div>
        </div>
      </Modal>

      <Modal
        isOpen={showDetailsModal}
        onClose={() => setShowDetailsModal(false)}
        title="Database Details"
        size="md"
      >
        {selectedDatabase ? (
          <div className="space-y-3 text-sm">
            <div className="flex justify-between">
              <span className="text-norse-silver">Name:</span>
              <span className="text-white font-medium">{selectedDatabase.name}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-norse-silver">Status:</span>
              <span className="text-white font-medium">{selectedDatabase.status}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-norse-silver">Nodes:</span>
              <span className="text-white font-medium">{selectedDatabase.nodeCount.toLocaleString()}</span>
            </div>
            <div className="flex justify-between">
              <span className="text-norse-silver">Edges:</span>
              <span className="text-white font-medium">{selectedDatabase.edgeCount.toLocaleString()}</span>
            </div>
          </div>
        ) : (
          <div className="text-norse-silver">No database selected.</div>
        )}
      </Modal>
    </PageLayout>
  );
}
