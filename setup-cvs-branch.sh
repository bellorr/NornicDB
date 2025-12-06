#!/bin/bash
# Setup script for CVS branch tracking
# ✅ CVS repository is now created and configured!

echo "CVS branch tracking is already set up!"

# Display current configuration
echo ""
echo "Remote configuration:"
git remote -v

echo ""
echo "Branch configuration:"
git branch -vv

echo ""
echo "✅ Setup complete!"
echo ""
echo "Usage:"
echo "  git checkout main        # Work on personal repository"
echo "  git push origin main     # Push to personal GitHub (orneryd/NornicDB)"
echo ""
echo "  git checkout cvs-main    # Work on corporate repository"
echo "  git push cvs main        # Push to corporate GitHub (cvs-health-source-code/NornicDB-Internal)"