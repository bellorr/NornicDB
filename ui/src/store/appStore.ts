import { create } from 'zustand';
import { api } from '../utils/api';
import type { DatabaseStats, SearchResult, CypherResponse } from '../utils/api';

// Similar results for inline expansion
interface SimilarExpansion {
  nodeId: string;
  results: SearchResult[];
  loading: boolean;
}

interface AppState {
  // Auth
  isAuthenticated: boolean;
  username: string | null;
  authLoading: boolean;
  
  // Database
  stats: DatabaseStats | null;
  connected: boolean;
  /** Selected database for queries (null = use server default). */
  selectedDatabase: string | null;
  /** List of database names for the dropdown (user-visible, excludes system). */
  databaseList: string[];
  setSelectedDatabase: (db: string | null) => void;
  fetchDatabases: () => Promise<void>;
  
  // Query
  cypherQuery: string;
  cypherResult: CypherResponse | null;
  queryLoading: boolean;
  queryError: string | null;
  queryHistory: string[];
  
  // Search
  searchQuery: string;
  searchResults: SearchResult[];
  searchLoading: boolean;
  searchError: string | null;
  
  // Selected
  selectedNode: SearchResult | null;
  selectedNodeIds: Set<string>;
  
  // Similar - inline expansion
  expandedSimilar: SimilarExpansion | null;
  
  // Actions
  checkAuth: () => Promise<void>;
  login: (username: string, password: string) => Promise<{ success: boolean; error?: string }>;
  logout: () => Promise<void>;
  fetchStats: () => Promise<void>;
  setCypherQuery: (query: string) => void;
  executeCypher: () => Promise<void>;
  setSearchQuery: (query: string) => void;
  executeSearch: () => Promise<void>;
  setSelectedNode: (node: SearchResult | null) => void;
  toggleNodeSelection: (nodeId: string) => void;
  selectAllNodes: (nodeIds: string[]) => void;
  clearNodeSelection: () => void;
  findSimilar: (nodeId: string) => Promise<void>;
  collapseSimilar: () => void;
}

export const useAppStore = create<AppState>((set, get) => ({
  // Initial state
  isAuthenticated: false,
  username: null,
  authLoading: true,
  stats: null,
  connected: false,
  selectedDatabase: null,
  databaseList: [],
  cypherQuery: 'MATCH (n) RETURN n LIMIT 25',
  cypherResult: null,
  queryLoading: false,
  queryError: null,
  queryHistory: [],
  searchQuery: '',
  searchResults: [],
  searchLoading: false,
  searchError: null,
  selectedNode: null,
  selectedNodeIds: new Set<string>(),
  expandedSimilar: null,

  // Auth actions
  checkAuth: async () => {
    set({ authLoading: true });
    const result = await api.checkAuth();
    set({
      isAuthenticated: result.authenticated,
      username: result.user || null,
      authLoading: false,
    });
  },

  login: async (username, password) => {
    const result = await api.login(username, password);
    if (result.success) {
      set({ isAuthenticated: true, username });
    }
    return result;
  },

  logout: async () => {
    await api.logout();
    set({ isAuthenticated: false, username: null });
  },

  // Database actions
  fetchStats: async () => {
    try {
      const stats = await api.getStatus();
      set({ stats, connected: true });
    } catch {
      set({ connected: false });
    }
  },

  setSelectedDatabase: (db) => set({ selectedDatabase: db }),

  fetchDatabases: async () => {
    try {
      const names = await api.listDatabaseNames();
      set({ databaseList: names });
    } catch {
      set({ databaseList: [] });
    }
  },

  // Query actions
  setCypherQuery: (query) => set({ cypherQuery: query }),

  executeCypher: async () => {
    const { cypherQuery, queryHistory, selectedDatabase } = get();
    if (!cypherQuery.trim()) return;

    set({ queryLoading: true, queryError: null });
    try {
      const result = await api.executeCypher(cypherQuery, undefined, selectedDatabase ?? undefined);
      
      // Check for errors in response
      if (result.errors && result.errors.length > 0) {
        set({
          queryError: result.errors.map(e => e.message).join('\n'),
          queryLoading: false,
        });
        return;
      }
      
      // Add to history if not duplicate
      const newHistory = queryHistory.includes(cypherQuery)
        ? queryHistory
        : [cypherQuery, ...queryHistory.slice(0, 19)];
      
      set({
        cypherResult: result,
        queryLoading: false,
        queryHistory: newHistory,
      });
    } catch (err) {
      set({
        queryError: err instanceof Error ? err.message : 'Query failed',
        queryLoading: false,
      });
    }
  },

  // Search actions
  setSearchQuery: (query) => set({ searchQuery: query }),

  executeSearch: async () => {
    const { searchQuery, selectedDatabase } = get();
    if (!searchQuery.trim()) {
      set({ searchResults: [], searchError: null });
      return;
    }

    set({ searchLoading: true, searchError: null });
    try {
      const results = await api.search(
        searchQuery,
        20,
        undefined,
        selectedDatabase ?? undefined
      );
      set({ searchResults: results, searchLoading: false, searchError: null });
    } catch (err) {
      set({
        searchResults: [],
        searchLoading: false,
        searchError: err instanceof Error ? err.message : 'Search failed',
      });
    }
  },

  // Node actions
  setSelectedNode: (node) => set({ selectedNode: node }),

  toggleNodeSelection: (nodeId) => set((state) => {
    const newSet = new Set(state.selectedNodeIds);
    if (newSet.has(nodeId)) {
      newSet.delete(nodeId);
    } else {
      newSet.add(nodeId);
    }
    return { selectedNodeIds: newSet };
  }),

  selectAllNodes: (nodeIds) => set({ selectedNodeIds: new Set(nodeIds) }),

  clearNodeSelection: () => set({ selectedNodeIds: new Set<string>() }),

  // Find similar with inline expansion (doesn't replace search results)
  findSimilar: async (nodeId) => {
    const { selectedDatabase } = get();
    set({ expandedSimilar: { nodeId, results: [], loading: true } });
    try {
      const results = await api.findSimilar(
        nodeId,
        6,
        selectedDatabase ?? undefined
      );
      set({ expandedSimilar: { nodeId, results, loading: false } });
    } catch {
      set({ expandedSimilar: null });
    }
  },

  // Collapse the similar results
  collapseSimilar: () => set({ expandedSimilar: null }),
}));
