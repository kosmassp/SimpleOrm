select id, name, email, display_name, created_at, updated_at
from users
where email = @Email
