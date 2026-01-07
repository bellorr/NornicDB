import { useState, useEffect } from 'react';
import { PageLayout } from '../components/common/PageLayout';
import { PageHeader } from '../components/common/PageHeader';
import { FormInput } from '../components/common/FormInput';
import { Button } from '../components/common/Button';
import { Alert } from '../components/common/Alert';
import { Modal } from '../components/common/Modal';
import { api, Collection } from '../utils/api';
import { Plus, Trash2, Database, Info } from 'lucide-react';

export function Collections() {
  const [collections, setCollections] = useState<Collection[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [refreshing, setRefreshing] = useState(false);

  // Create collection state
  const [showCreateModal, setShowCreateModal] = useState(false);
  const [newName, setNewName] = useState('');
  const [newSize, setNewSize] = useState('128');
  const [newDistance, setNewDistance] = useState<'Cosine' | 'Euclidean' | 'Dot'>('Cosine');
  const [creating, setCreating] = useState(false);
  const [createError, setCreateError] = useState('');

  // Delete collection state
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [deleting, setDeleting] = useState(false);
  const [deleteError, setDeleteError] = useState('');

  // Collection details state
  const [selectedCollection, setSelectedCollection] = useState<Collection | null>(null);
  const [showDetailsModal, setShowDetailsModal] = useState(false);

  useEffect(() => {
    loadCollections();
  }, []);

  const loadCollections = async () => {
    try {
      setError('');
      const data = await api.listCollections();
      setCollections(data);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load collections');
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  };

  const handleRefresh = () => {
    setRefreshing(true);
    loadCollections();
  };

  const handleCreateCollection = async (e: React.FormEvent) => {
    e.preventDefault();
    setCreateError('');
    setCreating(true);

    try {
      const size = parseInt(newSize, 10);
      if (isNaN(size) || size <= 0) {
        throw new Error('Vector size must be a positive number');
      }

      await api.createCollection(newName.trim(), size, newDistance);
      setShowCreateModal(false);
      setNewName('');
      setNewSize('128');
      setNewDistance('Cosine');
      await loadCollections();
    } catch (err) {
      setCreateError(err instanceof Error ? err.message : 'Failed to create collection');
    } finally {
      setCreating(false);
    }
  };

  const handleDeleteCollection = async () => {
    if (!deleteTarget) return;

    setDeleteError('');
    setDeleting(true);

    try {
      await api.deleteCollection(deleteTarget);
      setDeleteTarget(null);
      await loadCollections();
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : 'Failed to delete collection');
    } finally {
      setDeleting(false);
    }
  };

  const handleViewDetails = async (collection: Collection) => {
    try {
      const info = await api.getCollection(collection.name);
      setSelectedCollection({
        ...collection,
        info: info as any,
      });
      setShowDetailsModal(true);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to load collection details');
    }
  };

  if (loading) {
    return (
      <PageLayout>
        <PageHeader title="Collections" backTo="/" backLabel="Back to Browser" />
        <div className="flex items-center justify-center min-h-[400px]">
          <div className="text-norse-silver">Loading collections...</div>
        </div>
      </PageLayout>
    );
  }

  return (
    <PageLayout>
      <PageHeader
        title="Collections"
        backTo="/"
        backLabel="Back to Browser"
        actions={
          <div className="flex items-center gap-2">
            <Button
              variant="secondary"
              onClick={handleRefresh}
              disabled={refreshing}
            >
              {refreshing ? 'Refreshing...' : 'Refresh'}
            </Button>
            <Button
              variant="primary"
              onClick={() => setShowCreateModal(true)}
              icon={Plus}
            >
              Create Collection
            </Button>
          </div>
        }
      />

      {/* Main Content */}
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

        {/* Collections List */}
        {collections.length === 0 ? (
          <div className="bg-norse-shadow border border-norse-rune rounded-lg p-12 text-center">
            <Database className="w-16 h-16 text-norse-silver mx-auto mb-4" />
            <h3 className="text-lg font-semibold text-white mb-2">No Collections</h3>
            <p className="text-norse-silver mb-6">
              Create your first collection to start storing vectors
            </p>
            <Button
              variant="primary"
              onClick={() => setShowCreateModal(true)}
              icon={Plus}
            >
              Create Collection
            </Button>
          </div>
        ) : (
          <div className="grid grid-cols-1 md:grid-cols-2 lg:grid-cols-3 gap-4">
            {collections.map((collection) => (
              <div
                key={collection.name}
                className="bg-norse-shadow border border-norse-rune rounded-lg p-4 hover:border-nornic-primary transition-colors"
              >
                <div className="flex items-start justify-between mb-3">
                  <div className="flex-1">
                    <h3 className="text-lg font-semibold text-white mb-1">
                      {collection.name}
                    </h3>
                    <div className="flex items-center gap-2 text-sm text-norse-silver">
                      <span
                        className={`px-2 py-1 rounded text-xs ${
                          collection.info.status === 'GREEN'
                            ? 'bg-green-900/30 text-green-400'
                            : collection.info.status === 'YELLOW'
                            ? 'bg-yellow-900/30 text-yellow-400'
                            : 'bg-red-900/30 text-red-400'
                        }`}
                      >
                        {collection.info.status}
                      </span>
                    </div>
                  </div>
                  <div className="flex items-center gap-1">
                    <div title="View details">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => handleViewDetails(collection)}
                        icon={Info}
                      >
                        <span className="sr-only">View details</span>
                      </Button>
                    </div>
                    <div title="Delete collection">
                      <Button
                        variant="ghost"
                        size="sm"
                        onClick={() => setDeleteTarget(collection.name)}
                        icon={Trash2}
                        className="text-red-400 hover:text-red-300 hover:bg-red-900/20"
                      >
                        <span className="sr-only">Delete collection</span>
                      </Button>
                    </div>
                  </div>
                </div>

                <div className="space-y-2 text-sm">
                  <div className="flex justify-between">
                    <span className="text-norse-silver">Points:</span>
                    <span className="text-white font-medium">
                      {collection.info.points_count.toLocaleString()}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-norse-silver">Dimensions:</span>
                    <span className="text-white font-medium">
                      {collection.info.config.params.vectors.size}
                    </span>
                  </div>
                  <div className="flex justify-between">
                    <span className="text-norse-silver">Distance:</span>
                    <span className="text-white font-medium">
                      {collection.info.config.params.vectors.distance}
                    </span>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </main>

      {/* Create Collection Modal */}
      <Modal
        isOpen={showCreateModal}
        onClose={() => {
          setShowCreateModal(false);
          setCreateError('');
          setNewName('');
          setNewSize('128');
          setNewDistance('Cosine');
        }}
        title="Create Collection"
      >
        <form onSubmit={handleCreateCollection} className="space-y-4">
          {createError && (
            <Alert type="error" message={createError} dismissible onDismiss={() => setCreateError('')} />
          )}

          <FormInput
            label="Collection Name"
            type="text"
            value={newName}
            onChange={setNewName}
            required
            placeholder="my_collection"
          />

          <FormInput
            label="Vector Size (Dimensions)"
            type="number"
            value={newSize}
            onChange={setNewSize}
            required
            placeholder="128"
          />

          <div>
            <label className="block text-sm font-medium text-white mb-2">
              Distance Metric
            </label>
            <select
              value={newDistance}
              onChange={(e) => setNewDistance(e.target.value as 'Cosine' | 'Euclidean' | 'Dot')}
              className="w-full px-3 py-2 bg-norse-shadow border border-norse-rune rounded text-white focus:outline-none focus:ring-2 focus:ring-nornic-primary"
            >
              <option value="Cosine">Cosine</option>
              <option value="Euclidean">Euclidean</option>
              <option value="Dot">Dot</option>
            </select>
          </div>

          <div className="flex justify-end gap-2 pt-4">
            <Button
              type="button"
              variant="secondary"
              onClick={() => {
                setShowCreateModal(false);
                setCreateError('');
                setNewName('');
                setNewSize('128');
                setNewDistance('Cosine');
              }}
            >
              Cancel
            </Button>
            <Button type="submit" variant="primary" disabled={creating}>
              {creating ? 'Creating...' : 'Create Collection'}
            </Button>
          </div>
        </form>
      </Modal>

      {/* Delete Confirmation Modal */}
      <Modal
        isOpen={deleteTarget !== null}
        onClose={() => {
          setDeleteTarget(null);
          setDeleteError('');
        }}
        title="Delete Collection"
      >
        <div className="space-y-4">
          {deleteError && (
            <Alert type="error" message={deleteError} dismissible onDismiss={() => setDeleteError('')} />
          )}

          <p className="text-white">
            Are you sure you want to delete the collection <strong>{deleteTarget || ''}</strong>?
          </p>
          <p className="text-norse-silver text-sm">
            This will permanently delete the collection and all its vectors. This action cannot be undone.
          </p>

          <div className="flex justify-end gap-2 pt-4">
            <Button
              type="button"
              variant="secondary"
              onClick={() => {
                setDeleteTarget(null);
                setDeleteError('');
              }}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="primary"
              onClick={handleDeleteCollection}
              disabled={deleting}
              className="bg-red-600 hover:bg-red-700"
            >
              {deleting ? 'Deleting...' : 'Delete Collection'}
            </Button>
          </div>
        </div>
      </Modal>

      {/* Collection Details Modal */}
      <Modal
        isOpen={showDetailsModal}
        onClose={() => {
          setShowDetailsModal(false);
          setSelectedCollection(null);
        }}
        title={selectedCollection ? `Collection: ${selectedCollection.name}` : 'Collection Details'}
      >
        {selectedCollection && (
          <div className="space-y-4">
            <div className="grid grid-cols-2 gap-4">
              <div>
                <label className="text-sm font-medium text-norse-silver">Status</label>
                <div className="mt-1">
                  <span
                    className={`px-2 py-1 rounded text-sm ${
                      selectedCollection.info.status === 'GREEN'
                        ? 'bg-green-900/30 text-green-400'
                        : selectedCollection.info.status === 'YELLOW'
                        ? 'bg-yellow-900/30 text-yellow-400'
                        : 'bg-red-900/30 text-red-400'
                    }`}
                  >
                    {selectedCollection.info.status}
                  </span>
                </div>
              </div>
              <div>
                <label className="text-sm font-medium text-norse-silver">Points Count</label>
                <div className="mt-1 text-white font-medium">
                  {selectedCollection.info.points_count.toLocaleString()}
                </div>
              </div>
              <div>
                <label className="text-sm font-medium text-norse-silver">Vector Dimensions</label>
                <div className="mt-1 text-white font-medium">
                  {selectedCollection.info.config.params.vectors.size}
                </div>
              </div>
              <div>
                <label className="text-sm font-medium text-norse-silver">Distance Metric</label>
                <div className="mt-1 text-white font-medium">
                  {selectedCollection.info.config.params.vectors.distance}
                </div>
              </div>
            </div>
          </div>
        )}
      </Modal>
    </PageLayout>
  );
}

