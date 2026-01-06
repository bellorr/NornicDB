// NornicDB API Client

// Base path from environment variable (set at build time)
const BASE_PATH = import.meta.env.VITE_BASE_PATH || '';

export interface AuthConfig {
  devLoginEnabled: boolean;
  securityEnabled: boolean;
  oauthProviders: Array<{
    name: string;
    url: string;
    displayName: string;
  }>;
}

export interface DatabaseStats {
  status: string;
  server: {
    uptime_seconds: number;
    requests: number;
    errors: number;
    active: number;
  };
  database: {
    nodes: number;
    edges: number;
  };
}

export interface SearchResult {
  node: {
    id: string;
    labels: string[];
    properties: Record<string, unknown>;
    created_at: string;
  };
  score: number;
  rrf_score?: number;
  vector_rank?: number;
  bm25_rank?: number;
}

export interface CypherResponse {
  results: Array<{
    columns: string[];
    data: Array<{
      row: unknown[];
      meta: unknown[];
    }>;
  }>;
  errors: Array<{
    code: string;
    message: string;
  }>;
}

interface DiscoveryResponse {
  bolt_direct: string;
  bolt_routing: string;
  transaction: string;
  neo4j_version: string;
  neo4j_edition: string;
  default_database?: string; // NornicDB extension
}

class NornicDBClient {
  private defaultDatabase: string | null = null;

  // Get default database name from discovery endpoint
  private async getDefaultDatabase(): Promise<string> {
    // Return cached value if available
    if (this.defaultDatabase) {
      return this.defaultDatabase;
    }

    try {
      const res = await fetch(`${BASE_PATH}/`, { credentials: 'include' });
      if (res.ok) {
        const discovery: DiscoveryResponse = await res.json();
        // Cache the default database name
        this.defaultDatabase = discovery.default_database || 'nornic';
        return this.defaultDatabase;
      }
    } catch {
      // Fallback to default if discovery fails
    }

    // Fallback to NornicDB's default
    this.defaultDatabase = 'nornic';
    return this.defaultDatabase;
  }

  async getAuthConfig(): Promise<AuthConfig> {
    try {
      const res = await fetch(`${BASE_PATH}/auth/config`, { credentials: 'include' });
      if (res.ok) {
        return await res.json();
      }
      // Default config if endpoint doesn't exist
      return {
        devLoginEnabled: true,
        securityEnabled: false,
        oauthProviders: [],
      };
    } catch {
      // Auth disabled by default
      return {
        devLoginEnabled: true,
        securityEnabled: false,
        oauthProviders: [],
      };
    }
  }

  async checkAuth(): Promise<{ authenticated: boolean; user?: string }> {
    try {
      const res = await fetch(`${BASE_PATH}/auth/me`, { credentials: 'include' });
      if (res.ok) {
        const data = await res.json();
        return { authenticated: true, user: data.username };
      }
      return { authenticated: false };
    } catch {
      return { authenticated: false };
    }
  }

  async login(username: string, password: string): Promise<{ success: boolean; error?: string }> {
    try {
      const res = await fetch(`${BASE_PATH}/auth/token`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({ username, password }),
      });
      
      if (res.ok) {
        return { success: true };
      }
      
      const data = await res.json().catch(() => ({ message: 'Login failed' }));
      return { success: false, error: data.message || 'Invalid credentials' };
    } catch {
      return { success: false, error: 'Network error' };
    }
  }

  async logout(): Promise<void> {
    await fetch(`${BASE_PATH}/auth/logout`, {
      method: 'POST',
      credentials: 'include',
    });
  }

  async getHealth(): Promise<{ status: string; time: string }> {
    const res = await fetch(`${BASE_PATH}/health`);
    return await res.json();
  }

  async getStatus(): Promise<DatabaseStats> {
    const res = await fetch(`${BASE_PATH}/status`);
    return await res.json();
  }

  async search(query: string, limit: number = 10, labels?: string[]): Promise<SearchResult[]> {
    const res = await fetch(`${BASE_PATH}/nornicdb/search`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ query, limit, labels }),
    });
    return await res.json();
  }

  async findSimilar(nodeId: string, limit: number = 10): Promise<SearchResult[]> {
    const res = await fetch(`${BASE_PATH}/nornicdb/similar`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({ node_id: nodeId, limit }),
    });
    return await res.json();
  }

  async executeCypher(statement: string, parameters?: Record<string, unknown>): Promise<CypherResponse> {
    // Get default database name (will fetch from discovery endpoint if not cached)
    const dbName = await this.getDefaultDatabase();
    const res = await fetch(`${BASE_PATH}/db/${dbName}/tx/commit`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      credentials: 'include',
      body: JSON.stringify({
        statements: [{ statement, parameters }],
      }),
    });
    return await res.json();
  }

  async deleteNodes(nodeIds: string[]): Promise<{ success: boolean; deleted: number; errors: string[] }> {
    if (nodeIds.length === 0) {
      return { success: true, deleted: 0, errors: [] };
    }

    const dbName = await this.getDefaultDatabase();
    
    try {
      // First, verify the nodes exist before deleting (safety check)
      const verifyStatement = `MATCH (n) WHERE id(n) IN $ids RETURN id(n) as nodeId, elementId(n) as elementId`;
      const verifyRes = await fetch(`${BASE_PATH}/db/${dbName}/tx/commit`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          statements: [{ statement: verifyStatement, parameters: { ids: nodeIds } }],
        }),
      });
      
      const verifyResult: CypherResponse = await verifyRes.json();
      const foundCount = verifyResult.results[0]?.data?.length || 0;
      
      if (foundCount === 0) {
        return {
          success: false,
          deleted: 0,
          errors: [
            `None of the requested nodes were found. ` +
            `Requested IDs: ${nodeIds.join(', ')}. ` +
            `This may indicate the nodes were already deleted or the IDs are incorrect.`
          ],
        };
      }
      
      if (foundCount !== nodeIds.length) {
        return {
          success: false,
          deleted: 0,
          errors: [
            `Only ${foundCount} of ${nodeIds.length} requested nodes were found. ` +
            `Requested IDs: ${nodeIds.join(', ')}. ` +
            `Some nodes may not exist.`
          ],
        };
      }
      
      // Use bulk delete with id(n) IN $ids - verified by unit tests to work correctly
      // This is much more efficient than deleting one by one
      // The UI extracts internal IDs from elementId, which id(n) matches perfectly
      const statement = `MATCH (n) WHERE id(n) IN $ids DETACH DELETE n RETURN count(n) as deleted`;
      const parameters = { ids: nodeIds };

      const res = await fetch(`${BASE_PATH}/db/${dbName}/tx/commit`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          statements: [{ statement, parameters }],
        }),
      });

      const result: CypherResponse = await res.json();
      
      if (result.errors && result.errors.length > 0) {
        return {
          success: false,
          deleted: 0,
          errors: result.errors.map(e => e.message),
        };
      }

      const deleted = result.results[0]?.data[0]?.row[0] as number || 0;
      
      // CRITICAL: If more nodes were deleted than requested, this is a serious bug
      // The WHERE clause should have filtered correctly - this indicates a query issue
      if (deleted > nodeIds.length) {
        return {
          success: false,
          deleted,
          errors: [
            `CRITICAL: Expected to delete ${nodeIds.length} nodes, but ${deleted} were deleted. ` +
            `This indicates the WHERE clause did not filter correctly. ` +
            `Requested IDs: ${nodeIds.join(', ')}`
          ],
        };
      }
      
      // If fewer nodes were deleted, some may not exist
      if (deleted < nodeIds.length) {
        return {
          success: false,
          deleted,
          errors: [
            `Expected to delete ${nodeIds.length} nodes, but only ${deleted} were deleted. ` +
            `Some nodes may not exist. Requested IDs: ${nodeIds.join(', ')}`
          ],
        };
      }

      return {
        success: true,
        deleted,
        errors: [],
      };
    } catch (err) {
      return {
        success: false,
        deleted: 0,
        errors: [err instanceof Error ? err.message : 'Unknown error'],
      };
    }
  }

  async updateNodeProperties(nodeId: string, properties: Record<string, unknown>): Promise<{ success: boolean; error?: string }> {
    const dbName = await this.getDefaultDatabase();
    
    // Build SET clause
    const setParts: string[] = [];
    const parameters: Record<string, unknown> = { nodeId };
    let paramIndex = 0;
    
    for (const [key, value] of Object.entries(properties)) {
      const paramName = `p${paramIndex}`;
      setParts.push(`n.${key} = $${paramName}`);
      parameters[paramName] = value;
      paramIndex++;
    }

    if (setParts.length === 0) {
      return { success: true };
    }

    const statement = `MATCH (n) WHERE id(n) = $nodeId OR n.id = $nodeId SET ${setParts.join(', ')} RETURN n`;
    
    try {
      const res = await fetch(`${BASE_PATH}/db/${dbName}/tx/commit`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'include',
        body: JSON.stringify({
          statements: [{ statement, parameters }],
        }),
      });

      const result: CypherResponse = await res.json();
      
      if (result.errors && result.errors.length > 0) {
        return {
          success: false,
          error: result.errors.map(e => e.message).join('; '),
        };
      }

      return { success: true };
    } catch (err) {
      return {
        success: false,
        error: err instanceof Error ? err.message : 'Failed to update node',
      };
    }
  }
}

export const api = new NornicDBClient();
