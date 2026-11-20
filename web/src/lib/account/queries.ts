// Global account documents. Every field selects under the `account` namespace.
const ACCOUNT_FIELDS = `handle displayName profilePublic createdAt handleChangeableAt hasPassword`;

export const ACCOUNT_QUERY = `
  query Account {
    account {
      me { ${ACCOUNT_FIELDS} }
      authProviders { id name }
    }
  }
`;

export const PROFILE_QUERY = `
  query Profile($handle: String!) {
    account {
      profile(handle: $handle) {
        handle
        displayName
        createdAt
        isMine
      }
    }
  }
`;

export const LOGOUT_MUTATION = `
  mutation Logout {
    account { logout }
  }
`;

/**
 * Username/password sign-up and sign-in. Both are ordinary mutations: the
 * server sets the login cookie on the response. They are separate documents
 * because every root field of one document runs.
 */
export const REGISTER_WITH_PASSWORD_MUTATION = `
  mutation RegisterWithPassword($handle: String!, $password: String!) {
    account { registerWithPassword(handle: $handle, password: $password) { ${ACCOUNT_FIELDS} } }
  }
`;

export const LOG_IN_WITH_PASSWORD_MUTATION = `
  mutation LogInWithPassword($handle: String!, $password: String!) {
    account { logInWithPassword(handle: $handle, password: $password) { ${ACCOUNT_FIELDS} } }
  }
`;

export const UPDATE_ACCOUNT_MUTATION = `
  mutation UpdateAccount($input: Global_AccountInput!) {
    account { updateAccount(input: $input) { ${ACCOUNT_FIELDS} } }
  }
`;

export const CHANGE_PASSWORD_MUTATION = `
  mutation ChangePassword($currentPassword: String!, $newPassword: String!) {
    account { changePassword(currentPassword: $currentPassword, newPassword: $newPassword) }
  }
`;

export const DELETE_ACCOUNT_MUTATION = `
  mutation DeleteAccount($confirmHandle: String!) {
    account { deleteAccount(confirmHandle: $confirmHandle) }
  }
`;
